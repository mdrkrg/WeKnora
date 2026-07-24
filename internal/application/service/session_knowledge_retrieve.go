package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// RetrieveKnowledge executes the stateless retrieval-only path. It deliberately
// delegates the core search stages to SearchKnowledge, which does not create
// sessions or write messages.
func (s *sessionService) RetrieveKnowledge(ctx context.Context, req *types.KnowledgeRetrieveRequest) (*types.KnowledgeRetrieveData, error) {
	ctx = context.WithValue(ctx, retrieveExpansionContextKey{}, req.EnableQueryExpansion == nil || *req.EnableQueryExpansion)
	query := req.Query
	intent := types.IntentKBSearch
	understand := req.EnableQueryUnderstand == nil || *req.EnableQueryUnderstand
	if understand && s.eventManager != nil {
		modelID, modelErr := s.ResolveKnowledgeQAModel(ctx, strings.TrimSpace(req.ChatModelID))
		if modelErr != nil {
			return nil, modelErr
		}
		manage := &types.ChatManage{PipelineRequest: types.PipelineRequest{Query: req.Query, ChatModelID: modelID, QueryUnderstandModelID: modelID, EnableRewrite: s.cfg.Conversation.EnableRewrite, MaxRounds: 0}, PipelineState: types.PipelineState{RewriteQuery: req.Query, Intent: types.IntentKBSearch}}
		for _, message := range req.History {
			if message.Role == "user" {
				manage.History = append(manage.History, &types.History{Query: message.Content})
			} else {
				manage.History = append(manage.History, &types.History{Answer: message.Content})
			}
		}
		if modelID != "" {
			if err := s.eventManager.Trigger(ctx, types.QUERY_UNDERSTAND, manage); err == nil {
				if strings.TrimSpace(manage.RewriteQuery) != "" {
					query = manage.RewriteQuery
				}
				intent = manage.Intent
			}
		}
	}
	if !intent.NeedsKBRetrieval() {
		return &types.KnowledgeRetrieveData{RewriteQuery: query, Intent: intent, Results: []*types.KnowledgeRetrieveResult{}}, nil
	}
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
	results, err := s.SearchKnowledge(ctx, kbIDs, req.KnowledgeIDs, tagScopes, query)
	if err != nil {
		return nil, err
	}
	out := make([]*types.KnowledgeRetrieveResult, 0, len(results))
	for _, result := range results {
		out = append(out, projectRetrieveResult(result))
	}
	return &types.KnowledgeRetrieveData{RewriteQuery: query, Intent: intent, Results: out}, nil
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
	return &types.KnowledgeRetrieveResult{ID: result.ID, Content: result.Content, KnowledgeID: result.KnowledgeID, ChunkIndex: result.ChunkIndex, KnowledgeTitle: result.KnowledgeTitle, StartAt: result.StartAt, EndAt: result.EndAt, Score: result.Score, MatchType: retrieveMatchType(result.MatchType), SubChunkID: subChunkID, Metadata: metadata, ChunkType: result.ChunkType, ParentChunkID: result.ParentChunkID, ImageInfo: result.ImageInfo, KnowledgeFilename: result.KnowledgeFilename, KnowledgeSource: result.KnowledgeSource, KnowledgeChannel: result.KnowledgeChannel, ChunkMetadata: chunkMetadata, MatchedContent: result.MatchedContent, KnowledgeDescription: result.KnowledgeDescription, KnowledgeBaseID: result.KnowledgeBaseID}
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
