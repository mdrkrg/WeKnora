package service

import (
	"context"
	"fmt"
	"strings"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/types"
)

// RetrieveKnowledge executes the stateless retrieval-only pipeline.
// It runs query understanding, parallel chunk+entity search, rerank, merge,
// and top-K filtering on a single ChatManage instance — matching the spec
// pipeline (docs/knowledge-retrieve-spec.md §4.1) and the stateless chat path.
func (s *sessionService) RetrieveKnowledge(ctx context.Context, req *types.KnowledgeRetrieveRequest) (*types.KnowledgeRetrieveData, error) {
	ctx = context.WithValue(ctx, retrieveExpansionContextKey{}, req.EnableQueryExpansion == nil || *req.EnableQueryExpansion)

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
			EnableQueryExpansion: retrieveExpansionFromContext(ctx),
			TenantID:             tenantID,
		},
		PipelineState: types.PipelineState{
			RewriteQuery: req.Query,
			Intent:       types.IntentKBSearch,
		},
	}

	// Resolve rerank model
	if models, err := s.modelService.ListModels(ctx); err == nil {
		if rc != nil && rc.RerankModelID != "" {
			chatManage.RerankModelID = rc.RerankModelID
		} else {
			for _, model := range models {
				if model != nil && model.Type == types.ModelTypeRerank {
					chatManage.RerankModelID = model.ID
					break
				}
			}
		}
	}

	// ---- query understand (when enabled) ----
	understand := req.EnableQueryUnderstand == nil || *req.EnableQueryUnderstand
	if understand && s.eventManager != nil {
		modelID, modelErr := s.ResolveKnowledgeQAModel(ctx, strings.TrimSpace(req.ChatModelID))
		if modelErr != nil {
			return nil, modelErr
		}
		chatManage.ChatModelID = modelID
		chatManage.QueryUnderstandModelID = modelID
		chatManage.EnableRewrite = s.cfg.Conversation.EnableRewrite

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
			// Degrade gracefully per spec §5.3: on failure, RewriteQuery=original,
			// Intent=kb_search (already set in pipeline state defaults).
		}
	}

	// After query understand, check whether retrieval is needed.
	// If query understand failed, chatManage still holds the original fallbacks.
	if !chatManage.NeedsRetrieval() {
		return &types.KnowledgeRetrieveData{
			RewriteQuery: chatManage.RewriteQuery,
			Intent:       chatManage.Intent,
			Results:      []*types.KnowledgeRetrieveResult{},
		}, nil
	}

	// ---- search pipeline: CHUNK_SEARCH_PARALLEL → CHUNK_RERANK → CHUNK_MERGE → FILTER_TOP_K ----
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

type retrieveExpansionContextKey struct{}

func retrieveExpansionFromContext(ctx context.Context) bool {
	value, ok := ctx.Value(retrieveExpansionContextKey{}).(bool)
	return ok && value
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
	return &types.KnowledgeRetrieveResult{ID: result.ID, Content: result.Content, KnowledgeID: result.KnowledgeID, ChunkIndex: result.ChunkIndex, KnowledgeTitle: result.KnowledgeTitle, StartAt: result.StartAt, EndAt: result.EndAt, Score: result.Score, MatchType: retrieveMatchType(result.MatchType), SubChunkID: subChunkID, Metadata: metadata, ChunkType: result.ChunkType, ParentChunkID: result.ParentChunkID, ImageInfo: result.ImageInfo, KnowledgeFilename: result.KnowledgeFilename, KnowledgeSource: result.KnowledgeSource, KnowledgeChannel: result.KnowledgeChannel, ChunkMetadata: chunkMetadata, MatchedContent: result.MatchedContent, KnowledgeDescription: result.KnowledgeDescription, KnowledgeBaseID: result.KnowledgeBaseID, ContentSegments: contentSegments}
}

func retrieveMatchType(mt types.MatchType) string {
	labels := []string{"vector", "keyword", "nearby_chunk", "history", "parent_chunk", "relation_chunk", "graph", "web_search", "direct_load", "data_analysis"}
	if int(mt) >= 0 && int(mt) < len(labels) {
		return labels[mt]
	}
	return "unknown"
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
