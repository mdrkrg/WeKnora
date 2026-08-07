package session

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeRetrieveWireRequest is the HTTP wire format for
// POST /api/v1/knowledge-retrieve. QARequest carries no JSON tags, so the
// retrieve request uses a separate Wire DTO for binding; the handler converts
// it to types.KnowledgeRetrieveRequest (which embeds QARequest) before
// calling the service.
type KnowledgeRetrieveWireRequest struct {
	Query                 string                 `json:"query"`                  // Search query text
	KnowledgeBaseID       string                 `json:"knowledge_base_id"`      // Single KB (backward compat); merged with KnowledgeBaseIDs
	KnowledgeBaseIDs      []string               `json:"knowledge_base_ids"`     // Multi-KB search
	KnowledgeIDs          []string               `json:"knowledge_ids"`          // Specific knowledge (file) IDs
	TagIDs                []string               `json:"tag_ids"`                // KB-local tag filter
	MentionedItems        []types.MentionedItem  `json:"mentioned_items"`        // Scoped tag mentions (type="tag" only)
	EnableQueryUnderstand *bool                  `json:"enable_query_understand"` // Query-understand toggle (nil = true)
	EnableQueryExpansion  *bool                  `json:"enable_query_expansion"`  // Query-expansion toggle (nil = true)
	ChatModelID           string                 `json:"chat_model_id"`           // Query-understand model override
	RerankModelID         string                 `json:"rerank_model_id,omitempty"` // Rerank model override
	History               []types.HistoryMessage `json:"history"`                 // Multi-turn history for query rewrite
}

// toServiceRequest converts the Wire DTO into the service-layer request
// contract (types.KnowledgeRetrieveRequest, which embeds QARequest).
func (w KnowledgeRetrieveWireRequest) toServiceRequest() types.KnowledgeRetrieveRequest {
	return types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{
			Query:           w.Query,
			KnowledgeBaseIDs: w.KnowledgeBaseIDs,
			KnowledgeIDs:     w.KnowledgeIDs,
			RerankModelID:    w.RerankModelID,
		},
		KnowledgeBaseID:       w.KnowledgeBaseID,
		TagIDs:                w.TagIDs,
		MentionedItems:        w.MentionedItems,
		EnableQueryUnderstand: w.EnableQueryUnderstand,
		EnableQueryExpansion:  w.EnableQueryExpansion,
		ChatModelID:           w.ChatModelID,
		History:               w.History,
	}
}
