package service

import (
	"context"
	"fmt"
	"strings"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/types"
)

// RetrieveKnowledge executes the retrieval-only pipeline:
// query understanding -> parallel chunk+entity search -> rerank -> merge ->
// filter top K. It runs on a single ChatManage instance and returns the
// projected results plus the rewrite query and classified intent.
func (s *sessionService) RetrieveKnowledge(ctx context.Context, req *types.KnowledgeRetrieveRequest) (*types.KnowledgeRetrieveData, error) {
	// ---- resolve search scope ----
	kbIDs := append([]string(nil), req.KnowledgeBaseIDs...)
	if strings.TrimSpace(req.KnowledgeBaseID) != "" {
		kbIDs = append(kbIDs, req.KnowledgeBaseID)
	}
	kbIDs = uniqueRetrieveStrings(kbIDs)

	tagIDsByKB := make(map[string][]string)
	for _, item := range req.MentionedItems {
		if item.Type != "tag" {
			continue
		}
		if !containsRetrieveString(kbIDs, item.KBID) {
			kbIDs = append(kbIDs, item.KBID)
		}
		tagIDsByKB[item.KBID] = appendUniqueRetrieve(tagIDsByKB[item.KBID], item.ID)
	}
	if len(req.TagIDs) > 0 {
		for _, kbID := range kbIDs {
			tagIDsByKB[kbID] = appendUniqueRetrieve(tagIDsByKB[kbID], req.TagIDs...)
		}
	}
	tagScopes := make([]types.TagScope, 0, len(tagIDsByKB))
	for kbID, tagIDs := range tagIDsByKB {
		if kbID != "" && len(tagIDs) > 0 {
			tagScopes = append(tagScopes, types.TagScope{KnowledgeBaseID: kbID, TagIDs: tagIDs})
		}
	}

	// Get tenant
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("tenant ID not found in context")
	}

	// Build search targets
	searchTargets, err := s.buildSearchTargets(ctx, tenantID, kbIDs, req.KnowledgeIDs, tagScopes)
	if err != nil {
		return nil, err
	}
	if len(searchTargets) == 0 {
		return &types.KnowledgeRetrieveData{RewriteQuery: req.Query, Intent: types.IntentKBSearch, Results: []*types.KnowledgeRetrieveResult{}}, nil
	}

	// ---- build unified ChatManage ----
	userID := types.SessionOwnerIDFromContext(ctx)

	var rc *types.RetrievalConfig
	if tenant, err := s.tenantService.GetTenantByID(ctx, tenantID); err == nil {
		rc = tenant.RetrievalConfig
	}

	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:                req.Query,
			UserID:               userID,
			KnowledgeBaseIDs:     kbIDs,
			KnowledgeIDs:         req.KnowledgeIDs,
			SearchTargets:        searchTargets,
			MaxRounds:            s.cfg.Conversation.MaxRounds,
			EmbeddingTopK:        rc.GetEffectiveEmbeddingTopK(),
			VectorThreshold:      rc.GetEffectiveVectorThreshold(),
			KeywordThreshold:     rc.GetEffectiveKeywordThreshold(),
			RerankTopK:           rc.GetEffectiveRerankTopK(),
			RerankThreshold:      rc.GetEffectiveRerankThreshold(),
			EnableQueryExpansion: req.EnableQueryExpansion == nil || *req.EnableQueryExpansion,
			TenantID:             tenantID,
		},
		PipelineState: types.PipelineState{
			RewriteQuery: req.Query,
			Intent:       types.IntentKBSearch,
		},
	}

	// Resolve rerank model: request override -> tenant config -> auto-detect.
	chatManage.RerankModelID, err = s.resolveRerankModelID(ctx, strings.TrimSpace(req.RerankModelID), "", rc)
	if err != nil {
		return nil, err
	}

	// ---- query understand (when enabled and rewrite allowed) ----
	// Per spec sec. 4.2 / 5.3: when the global enable_rewrite config is false,
	// the whole stage is skipped - no model resolution, no LLM call, no
	// entity extraction, no history injection.
	understand := req.EnableQueryUnderstand == nil || *req.EnableQueryUnderstand
	if understand && s.cfg.Conversation.EnableRewrite && s.eventManager != nil {
		modelID, modelErr := s.ResolveKnowledgeQAModel(ctx, strings.TrimSpace(req.ChatModelID))
		if modelErr != nil {
			return nil, modelErr
		}
		chatManage.ChatModelID = modelID
		chatManage.QueryUnderstandModelID = modelID
		chatManage.EnableRewrite = true

		// Inject request history for multi-turn rewrite
		for _, message := range req.History {
			if message.Role == "user" {
				chatManage.History = append(chatManage.History, &types.History{Query: message.Content})
			} else {
				chatManage.History = append(chatManage.History, &types.History{Answer: message.Content})
			}
		}

		if modelID != "" {
			_ = s.eventManager.Trigger(ctx, types.QUERY_UNDERSTAND, chatManage)
			// Degrade gracefully per spec sec. 5.3: on failure, RewriteQuery=original,
			// Intent=kb_search (already set in pipeline state defaults).
		}

		// Spec sec. 4.2/5.3: an unknown intent from the LLM must fall back to
		// kb_search instead of being echoed back or silently skipping retrieval.
		if !isKnownRetrieveIntent(chatManage.Intent) {
			chatManage.Intent = types.IntentKBSearch
		}
	}

	// After query understand, check whether retrieval is needed.
	if !chatManage.NeedsRetrieval() {
		return &types.KnowledgeRetrieveData{
			RewriteQuery: chatManage.RewriteQuery,
			Intent:       chatManage.Intent,
			Results:      []*types.KnowledgeRetrieveResult{},
		}, nil
	}

	// ---- search pipeline: CHUNK_SEARCH_PARALLEL -> CHUNK_RERANK -> CHUNK_MERGE -> FILTER_TOP_K ----
	searchEvents := []types.EventType{
		types.CHUNK_SEARCH_PARALLEL,
		types.CHUNK_RERANK,
		types.CHUNK_MERGE,
		types.FILTER_TOP_K,
	}

	for _, event := range searchEvents {
		err := s.eventManager.Trigger(ctx, event, chatManage)
		if err == chatpipeline.ErrSearchNothing {
			return &types.KnowledgeRetrieveData{
				RewriteQuery: chatManage.RewriteQuery,
				Intent:       chatManage.Intent,
				Results:      []*types.KnowledgeRetrieveResult{},
			}, nil
		}
		if err != nil {
			return nil, err.Err
		}
	}

	// ---- project results ----
	out := make([]*types.KnowledgeRetrieveResult, 0, len(chatManage.MergeResult))
	for _, result := range chatManage.MergeResult {
		out = append(out, projectRetrieveResult(result))
	}
	return &types.KnowledgeRetrieveData{
		RewriteQuery: chatManage.RewriteQuery,
		Intent:       chatManage.Intent,
		Results:      out,
	}, nil
}

func projectRetrieveResult(result *types.SearchResult) *types.KnowledgeRetrieveResult {
	metadata := map[string]string{}
	subChunkID := []string{}
	chunkMetadata := types.JSON([]byte("{}"))
	if result == nil {
		return &types.KnowledgeRetrieveResult{Metadata: metadata, SubChunkID: subChunkID, ChunkMetadata: chunkMetadata}
	}
	if result.Metadata != nil {
		metadata = result.Metadata
	}
	if result.SubChunkID != nil {
		subChunkID = result.SubChunkID
	}
	if len(result.ChunkMetadata) > 0 {
		chunkMetadata = result.ChunkMetadata
	}
	contentSegments := result.ContentSegments
	if contentSegments == nil {
		contentSegments = []types.ContentSegment{}
	}
	return &types.KnowledgeRetrieveResult{
		ID:                   result.ID,
		Content:              result.Content,
		KnowledgeID:          result.KnowledgeID,
		ChunkIndex:           result.ChunkIndex,
		KnowledgeTitle:       result.KnowledgeTitle,
		StartAt:              result.StartAt,
		EndAt:                result.EndAt,
		Score:                result.Score,
		MatchType:            retrieveMatchType(result.MatchType),
		SubChunkID:           subChunkID,
		Metadata:             metadata,
		ChunkType:            result.ChunkType,
		ParentChunkID:        result.ParentChunkID,
		ImageInfo:            result.ImageInfo,
		KnowledgeFilename:    result.KnowledgeFilename,
		KnowledgeSource:      result.KnowledgeSource,
		KnowledgeChannel:     result.KnowledgeChannel,
		ChunkMetadata:        chunkMetadata,
		MatchedContent:       result.MatchedContent,
		KnowledgeDescription: result.KnowledgeDescription,
		KnowledgeBaseID:      result.KnowledgeBaseID,
		ContentSegments:      contentSegments,
	}
}

func retrieveMatchType(mt types.MatchType) string {
	labels := []string{"vector", "keyword", "nearby_chunk", "history", "parent_chunk", "relation_chunk", "graph", "web_search", "direct_load", "data_analysis"}
	if int(mt) >= 0 && int(mt) < len(labels) {
		return labels[mt]
	}
	return "unknown"
}

// isKnownRetrieveIntent reports whether intent is one of the nine documented
// intents (spec sec. 3.3). Unknown values from the LLM fall back to kb_search.
func isKnownRetrieveIntent(intent types.QueryIntent) bool {
	switch intent {
	case types.IntentKBSearch, types.IntentWebSearch, types.IntentGreeting,
		types.IntentChitchat, types.IntentFollowUp, types.IntentImageOnly,
		types.IntentDocOnly, types.IntentSummarize, types.IntentClarification:
		return true
	default:
		return false
	}
}

func uniqueRetrieveStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" && !containsRetrieveString(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func containsRetrieveString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func appendUniqueRetrieve(values []string, additions ...string) []string {
	for _, addition := range additions {
		if addition != "" && !containsRetrieveString(values, addition) {
			values = append(values, addition)
		}
	}
	return values
}
