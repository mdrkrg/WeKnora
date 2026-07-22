package types

import "encoding/json"

// KnowledgeRetrieveRequest is the request contract for POST /api/v1/knowledge-retrieve.
type KnowledgeRetrieveRequest struct {
	Query                 string           `json:"query"`
	KnowledgeBaseID       string           `json:"knowledge_base_id,omitempty"`
	KnowledgeBaseIDs      []string         `json:"knowledge_base_ids,omitempty"`
	KnowledgeIDs          []string         `json:"knowledge_ids,omitempty"`
	TagIDs                []string         `json:"tag_ids,omitempty"`
	MentionedItems        []MentionedItem  `json:"mentioned_items,omitempty"`
	EnableQueryUnderstand *bool            `json:"enable_query_understand,omitempty"`
	EnableQueryExpansion  *bool            `json:"enable_query_expansion,omitempty"`
	ChatModelID           string           `json:"chat_model_id,omitempty"`
	History               []HistoryMessage `json:"history,omitempty"`
}

type KnowledgeRetrieveData struct {
	RewriteQuery string                     `json:"rewrite_query"`
	Intent       QueryIntent                `json:"intent"`
	Results      []*KnowledgeRetrieveResult `json:"results"`
}

// KnowledgeRetrieveResult is the stable response projection required by the retrieve API.
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
	return json.Marshal(resultAlias(r))
}

type KnowledgeRetrieveResponse struct {
	Success bool                    `json:"success"`
	Data    *KnowledgeRetrieveData  `json:"data,omitempty"`
	Error   *KnowledgeRetrieveError `json:"error,omitempty"`
}

type KnowledgeRetrieveError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}
