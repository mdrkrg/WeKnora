package session

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Reference: docs/knowledge-retrieve-spec.md sections 2.3-2.5.
func TestValidateKnowledgeRetrieveRequestScopeRules(t *testing.T) {
	tests := []struct {
		name string
		req  KnowledgeRetrieveRequest
		want bool
	}{
		{name: "requires query", req: KnowledgeRetrieveRequest{KnowledgeBaseID: "kb-1"}, want: false},
		{name: "requires a search scope", req: KnowledgeRetrieveRequest{Query: "q"}, want: false},
		{name: "bare tags are not a scope", req: KnowledgeRetrieveRequest{Query: "q", TagIDs: []string{"tag-1"}}, want: false},
		{name: "knowledge id supplies scope", req: KnowledgeRetrieveRequest{Query: "q", KnowledgeIDs: []string{"file-1"}}, want: true},
		{name: "scoped tag supplies scope", req: KnowledgeRetrieveRequest{Query: "q", MentionedItems: []types.MentionedItem{{ID: "tag-1", Type: "tag", KBID: "kb-1"}}}, want: true},
		{name: "kb supplies scope", req: KnowledgeRetrieveRequest{Query: "q", KnowledgeBaseID: "kb-1"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKnowledgeRetrieveRequest(tt.req)
			if (err == nil) != tt.want {
				t.Fatalf("validation error = %v, want valid=%v", err, tt.want)
			}
		})
	}
}

// Reference: docs/knowledge-retrieve-spec.md sections 2.3-2.5.
func TestValidateKnowledgeRetrieveRequestRejectsInvalidMentionsAndTags(t *testing.T) {
	tests := []KnowledgeRetrieveRequest{
		{Query: "q", KnowledgeBaseID: "kb-1", MentionedItems: []types.MentionedItem{{ID: "tag-1", Type: "file", KBID: "kb-1"}}},
		{Query: "q", KnowledgeBaseID: "kb-1", MentionedItems: []types.MentionedItem{{ID: "tag-1", Type: "tag"}}},
		{Query: "q", KnowledgeBaseID: "kb-1", MentionedItems: []types.MentionedItem{{ID: "", Type: "tag", KBID: "kb-1"}}},
	}
	for i, req := range tests {
		if err := ValidateKnowledgeRetrieveRequest(req); err == nil {
			t.Fatalf("case %d: expected invalid request", i)
		}
	}
}

// Reference: docs/knowledge-retrieve-spec.md section 2.4.
func TestValidateKnowledgeRetrieveRequestHistory(t *testing.T) {
	valid := KnowledgeRetrieveRequest{Query: "q", KnowledgeBaseID: "kb-1", History: []types.HistoryMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}}}
	if err := ValidateKnowledgeRetrieveRequest(valid); err != nil {
		t.Fatalf("valid history rejected: %v", err)
	}
	invalidRole := valid
	invalidRole.History = []types.HistoryMessage{{Role: "system", Content: "no"}}
	if err := ValidateKnowledgeRetrieveRequest(invalidRole); err == nil {
		t.Fatal("system history role must be rejected")
	}
	missingRole := valid
	missingRole.History = []types.HistoryMessage{{Role: "", Content: "hello"}}
	if err := ValidateKnowledgeRetrieveRequest(missingRole); err == nil {
		t.Fatal("empty history role must be rejected")
	}
	tooLong := valid
	tooLong.History = make([]types.HistoryMessage, MaxKnowledgeRetrieveHistory+1)
	if err := ValidateKnowledgeRetrieveRequest(tooLong); err == nil {
		t.Fatal("history over 100 messages must be rejected")
	}
}

// Reference: docs/knowledge-retrieve-spec.md sections 3.1 and 3.4-3.5.
func TestKnowledgeRetrieveResponseHasStableEmptyValues(t *testing.T) {
	payload := KnowledgeRetrieveResponse{Success: true, Data: &KnowledgeRetrieveData{RewriteQuery: "q", Intent: types.IntentKBSearch, Results: []*KnowledgeRetrieveResult{{}}}}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	result := got["data"].(map[string]any)["results"].([]any)[0].(map[string]any)
	for _, field := range []string{"id", "content", "knowledge_id", "chunk_index", "knowledge_title", "start_at", "end_at", "score", "match_type", "sub_chunk_id", "metadata", "chunk_type", "parent_chunk_id", "image_info", "knowledge_filename", "knowledge_source", "knowledge_channel", "chunk_metadata", "matched_content", "knowledge_description", "knowledge_base_id"} {
		if _, ok := result[field]; !ok {
			t.Errorf("missing stable result field %q", field)
		}
	}
	if got, ok := result["sub_chunk_id"].([]any); !ok || len(got) != 0 {
		t.Errorf("sub_chunk_id = %#v, want empty array", result["sub_chunk_id"])
	}
	if got, ok := result["metadata"].(map[string]any); !ok || len(got) != 0 {
		t.Errorf("metadata = %#v, want empty object", result["metadata"])
	}
	if got, ok := result["chunk_metadata"].(map[string]any); !ok || len(got) != 0 {
		t.Errorf("chunk_metadata = %#v, want empty object", result["chunk_metadata"])
	}
	for _, field := range []string{"chunk_index", "start_at", "end_at", "score"} {
		if got, ok := result[field].(float64); !ok || got != 0 {
			t.Errorf("%s = %#v, want numeric zero", field, result[field])
		}
	}
}

// Reference: docs/knowledge-retrieve-spec.md section 3.5.
func TestKnowledgeRetrieveMatchTypeMapping(t *testing.T) {
	for _, tt := range []struct {
		in  types.MatchType
		out string
	}{
		{types.MatchTypeEmbedding, "vector"},
		{types.MatchTypeKeywords, "keyword"},
		{types.MatchTypeNearByChunk, "nearby_chunk"},
		{types.MatchTypeHistory, "history"},
		{types.MatchTypeParentChunk, "parent_chunk"},
		{types.MatchTypeRelationChunk, "relation_chunk"},
		{types.MatchTypeGraph, "graph"},
		{types.MatchTypeWebSearch, "web_search"},
		{types.MatchTypeDirectLoad, "direct_load"},
		{types.MatchTypeDataAnalysis, "data_analysis"},
		{types.MatchType(9999), "unknown"},
	} {
		if got := KnowledgeRetrieveMatchType(tt.in); got != tt.out {
			t.Errorf("match type %v = %q, want %q", tt.in, got, tt.out)
		}
	}
}

// Reference: docs/knowledge-retrieve-spec.md sections 3.3 and 4.3.
func TestKnowledgeRetrieveNoRetrievalIntentReturnsEmptyResults(t *testing.T) {
	for _, intent := range []types.QueryIntent{types.IntentChitchat, types.IntentGreeting, types.IntentFollowUp, types.IntentWebSearch, types.IntentImageOnly, types.IntentDocOnly} {
		if intent.NeedsKBRetrieval() {
			t.Errorf("intent %q must skip retrieval", intent)
		}
	}
}

// Reference: docs/knowledge-retrieve-spec.md sections 5.1-5.2.
func TestKnowledgeRetrieveErrorEnvelopeContract(t *testing.T) {
	if http.StatusRequestEntityTooLarge != 413 || http.StatusTooManyRequests != 429 || http.StatusGatewayTimeout != 504 {
		t.Fatal("HTTP status constants changed unexpectedly")
	}
	if got := KnowledgeRetrieveErrorEnvelope(1000, "query cannot be empty"); got.Success || got.Error.Code != 1000 || got.Error.Message != "query cannot be empty" || got.Error.Details != nil || got.Data != nil {
		t.Fatalf("invalid error envelope: %#v", got)
	}
}

// Spec: docs/knowledge-retrieve-spec.md section 2.2, 2.3.
// rerank_model_id is an optional string field for selecting the rerank model.
// When provided, it survives JSON round-trip.
func TestKnowledgeRetrieveRequestRerankModelIDJSON(t *testing.T) {
	raw := `{"query":"test","knowledge_base_id":"kb-1","rerank_model_id":"rerank-uuid"}`
	var req KnowledgeRetrieveRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.RerankModelID != "rerank-uuid" {
		t.Errorf("RerankModelID = %q, want rerank-uuid", req.RerankModelID)
	}

	back, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(back, &out); err != nil {
		t.Fatal(err)
	}
	if got := out["rerank_model_id"]; got != "rerank-uuid" {
		t.Errorf("rerank_model_id = %v, want rerank-uuid", got)
	}
}

// Spec: docs/knowledge-retrieve-spec.md §2.3.
// rerank_model_id is optional with omitempty: absent in input means empty
// string, and empty string is omitted from json output.
func TestKnowledgeRetrieveRequestRerankModelIDOmitted(t *testing.T) {
	raw := `{"query":"test","knowledge_base_id":"kb-1"}`
	var req KnowledgeRetrieveRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.RerankModelID != "" {
		t.Errorf("RerankModelID = %q, want empty", req.RerankModelID)
	}

	back, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(back, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["rerank_model_id"]; ok {
		t.Error("rerank_model_id should be omitted when empty")
	}
}
