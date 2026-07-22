package session

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const MaxKnowledgeRetrieveHistory = 100

type KnowledgeRetrieveRequest = types.KnowledgeRetrieveRequest
type KnowledgeRetrieveData = types.KnowledgeRetrieveData
type KnowledgeRetrieveResult = types.KnowledgeRetrieveResult
type KnowledgeRetrieveResponse = types.KnowledgeRetrieveResponse
type KnowledgeRetrieveError = types.KnowledgeRetrieveError

// ValidateKnowledgeRetrieveRequest validates the structural portion of the
// retrieve request. Resource ownership is checked while building search targets.
func ValidateKnowledgeRetrieveRequest(r types.KnowledgeRetrieveRequest) error {
	req := r
	if strings.TrimSpace(req.Query) == "" {
		return fmt.Errorf("query cannot be empty")
	}
	if len(req.History) > MaxKnowledgeRetrieveHistory {
		return fmt.Errorf("history exceeds maximum of %d messages", MaxKnowledgeRetrieveHistory)
	}
	for i, msg := range req.History {
		if msg.Role != "user" && msg.Role != "assistant" {
			return fmt.Errorf("history[%d].role must be user or assistant", i)
		}
	}
	hasScope := strings.TrimSpace(req.KnowledgeBaseID) != "" || len(nonEmpty(req.KnowledgeBaseIDs)) > 0 || len(nonEmpty(req.KnowledgeIDs)) > 0
	seenMention := make(map[string]string)
	for i, item := range req.MentionedItems {
		if item.Type != "tag" || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.KBID) == "" {
			return fmt.Errorf("mentioned_items[%d] must be a tag with non-empty id and kb_id", i)
		}
		if previous, ok := seenMention[item.ID]; ok && previous != item.KBID {
			return fmt.Errorf("tag %q is associated with multiple knowledge bases", item.ID)
		}
		seenMention[item.ID] = item.KBID
		hasScope = true
	}
	if !hasScope {
		return fmt.Errorf("at least one knowledge base, knowledge, or scoped tag is required")
	}
	return nil
}

// KnowledgeRetrieveMatchType converts internal match types at the response boundary.
func KnowledgeRetrieveMatchType(mt types.MatchType) string {
	switch mt {
	case types.MatchTypeEmbedding:
		return "vector"
	case types.MatchTypeKeywords:
		return "keyword"
	case types.MatchTypeNearByChunk:
		return "nearby_chunk"
	case types.MatchTypeHistory:
		return "history"
	case types.MatchTypeParentChunk:
		return "parent_chunk"
	case types.MatchTypeRelationChunk:
		return "relation_chunk"
	case types.MatchTypeGraph:
		return "graph"
	case types.MatchTypeWebSearch:
		return "web_search"
	case types.MatchTypeDirectLoad:
		return "direct_load"
	case types.MatchTypeDataAnalysis:
		return "data_analysis"
	default:
		return "unknown"
	}
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func KnowledgeRetrieveErrorEnvelope(code int, message string) types.KnowledgeRetrieveResponse {
	return types.KnowledgeRetrieveResponse{
		Success: false,
		Error:   &types.KnowledgeRetrieveError{Code: code, Message: message, Details: nil},
	}
}
