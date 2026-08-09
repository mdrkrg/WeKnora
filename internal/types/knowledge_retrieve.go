package types

import "encoding/json"

// KnowledgeRetrieveRequest is the service-layer request contract for
// POST /api/v1/knowledge-retrieve. It embeds QARequest to inherit shared
// pipeline fields (Query, KnowledgeBaseIDs, KnowledgeIDs, RerankModelID).
//
// QARequest carries no JSON tags, so HTTP binding uses a separate Wire DTO
// in the handler layer; this struct is populated by the handler after
// unmarshalling the request body.
type KnowledgeRetrieveRequest struct {
	QARequest

	// KnowledgeBaseID is a single-KB shorthand (backward compatible); merged
	// with KnowledgeBaseIDs by the handler/service.
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	// TagScopes are the resolved tag-constrained KB scopes (built by the
	// handler from tag_ids + mentioned_items, mirroring /knowledge-search).
	TagScopes []TagScope `json:"-"`
	// EnableQueryUnderstand controls LLM query understanding (rewrite + intent
	// + optional entity extraction). nil defaults to true.
	EnableQueryUnderstand *bool `json:"enable_query_understand,omitempty"`
	// EnableQueryExpansion controls local query expansion on low recall.
	// nil defaults to true.
	EnableQueryExpansion *bool `json:"enable_query_expansion,omitempty"`
	// ChatModelID overrides the KnowledgeQA model used for query understand.
	// Empty uses the tenant default. Ignored when query understand is off.
	ChatModelID string `json:"chat_model_id,omitempty"`
	// History is the conversation history used for multi-turn query rewriting.
	History []HistoryMessage `json:"history,omitempty"`
}

// KnowledgeRetrieveData is the top-level retrieve response payload.
type KnowledgeRetrieveData struct {
	RewriteQuery string                     `json:"rewrite_query"`
	Intent       QueryIntent                `json:"intent"`
	Results      []*KnowledgeRetrieveResult `json:"results"`
}

// KnowledgeRetrieveResult is the stable response projection required by the
// retrieve API.
type KnowledgeRetrieveResult struct {
	ID                   string            `json:"id"`
	Content              string            `json:"content"`
	KnowledgeID          string            `json:"knowledge_id"`
	ChunkIndex           int               `json:"chunk_index"`
	KnowledgeTitle       string            `json:"knowledge_title"`
	StartAt              int               `json:"start_at"`
	EndAt                int               `json:"end_at"`
	Score                float64           `json:"score"`
	MatchType            string            `json:"match_type"`
	SubChunkID           []string          `json:"sub_chunk_id"`
	Metadata             map[string]string `json:"metadata"`
	ChunkType            string            `json:"chunk_type"`
	ParentChunkID        string            `json:"parent_chunk_id"`
	ImageInfo            string            `json:"image_info"`
	KnowledgeFilename    string            `json:"knowledge_filename"`
	KnowledgeSource      string            `json:"knowledge_source"`
	KnowledgeChannel     string            `json:"knowledge_channel"`
	ChunkMetadata        JSON              `json:"chunk_metadata"`
	MatchedContent       string            `json:"matched_content"`
	KnowledgeDescription string            `json:"knowledge_description"`
	KnowledgeBaseID      string            `json:"knowledge_base_id"`
	ContentSegments      []ContentSegment  `json:"content_segments"`
}

// MarshalJSON guarantees the retrieve contract's non-null collection/object
// fields even when a zero-valued result reaches the response boundary.
func (r KnowledgeRetrieveResult) MarshalJSON() ([]byte, error) {
	type resultAlias KnowledgeRetrieveResult
	if r.SubChunkID == nil {
		r.SubChunkID = []string{}
	}
	if r.Metadata == nil {
		r.Metadata = map[string]string{}
	}
	if len(r.ChunkMetadata) == 0 {
		r.ChunkMetadata = JSON([]byte("{}"))
	}
	if r.ContentSegments == nil {
		r.ContentSegments = []ContentSegment{}
	}
	return json.Marshal(resultAlias(r))
}

// KnowledgeRetrieveResponse is the HTTP envelope for the retrieve endpoint.
type KnowledgeRetrieveResponse struct {
	Success bool                    `json:"success"`
	Data    *KnowledgeRetrieveData  `json:"data,omitempty"`
	Error   *KnowledgeRetrieveError `json:"error,omitempty"`
}

// KnowledgeRetrieveError is the structured error payload of the retrieve
// endpoint.
type KnowledgeRetrieveError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}
