package session

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Reference: docs/knowledge-retrieve-spec.md sections 2.3-2.5.
func TestValidateKnowledgeRetrieveRequestScopeRules(t *testing.T) {
	tests := []struct {
		name string
		req  KnowledgeRetrieveWireRequest
		want bool
	}{
		{name: "requires query", req: KnowledgeRetrieveWireRequest{KnowledgeBaseID: "kb-1"}, want: false},
		{name: "requires a search scope", req: KnowledgeRetrieveWireRequest{Query: "q"}, want: false},
		{name: "bare tags are not a scope", req: KnowledgeRetrieveWireRequest{Query: "q", TagIDs: []string{"tag-1"}}, want: false},
		{name: "knowledge id supplies scope", req: KnowledgeRetrieveWireRequest{Query: "q", KnowledgeIDs: []string{"file-1"}}, want: true},
		{name: "scoped tag supplies scope", req: KnowledgeRetrieveWireRequest{Query: "q", MentionedItems: []MentionedItemRequest{{ID: "tag-1", Type: "tag", KBID: "kb-1"}}}, want: true},
		{name: "kb supplies scope", req: KnowledgeRetrieveWireRequest{Query: "q", KnowledgeBaseID: "kb-1"}, want: true},
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
	tests := []KnowledgeRetrieveWireRequest{
		{Query: "q", KnowledgeBaseID: "kb-1", MentionedItems: []MentionedItemRequest{{ID: "tag-1", Type: "file", KBID: "kb-1"}}},
		{Query: "q", KnowledgeBaseID: "kb-1", MentionedItems: []MentionedItemRequest{{ID: "tag-1", Type: "tag"}}},
		{Query: "q", KnowledgeBaseID: "kb-1", MentionedItems: []MentionedItemRequest{{ID: "", Type: "tag", KBID: "kb-1"}}},
	}
	for i, req := range tests {
		if err := ValidateKnowledgeRetrieveRequest(req); err == nil {
			t.Fatalf("case %d: expected invalid request", i)
		}
	}
}

// Reference: docs/knowledge-retrieve-spec.md section 2.4.
func TestValidateKnowledgeRetrieveRequestHistory(t *testing.T) {
	valid := KnowledgeRetrieveWireRequest{Query: "q", KnowledgeBaseID: "kb-1", History: []types.HistoryMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}}}
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
	payload := types.KnowledgeRetrieveResponse{Success: true, Data: &types.KnowledgeRetrieveData{RewriteQuery: "q", Intent: types.IntentKBSearch, Results: []*types.KnowledgeRetrieveResult{{}}}}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	result := got["data"].(map[string]any)["results"].([]any)[0].(map[string]any)
	for _, field := range []string{"id", "content", "knowledge_id", "chunk_index", "knowledge_title", "start_at", "end_at", "score", "match_type", "sub_chunk_id", "metadata", "chunk_type", "parent_chunk_id", "image_info", "knowledge_filename", "knowledge_source", "knowledge_channel", "chunk_metadata", "matched_content", "knowledge_description", "knowledge_base_id", "content_segments"} {
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

// Reference: docs/knowledge-retrieve-spec.md sections 3.3 and 4.3.
func TestKnowledgeRetrieveNoRetrievalIntentReturnsEmptyResults(t *testing.T) {
	for _, intent := range []types.QueryIntent{types.IntentChitchat, types.IntentGreeting, types.IntentFollowUp, types.IntentWebSearch, types.IntentImageOnly, types.IntentDocOnly} {
		if intent.NeedsKBRetrieval() {
			t.Errorf("intent %q must skip retrieval", intent)
		}
	}
}

// Reference: docs/knowledge-retrieve-spec.md sections 2.3-2.5.
// Bare tag_ids are rejected across multiple KBs (400); they are only merged
// into a single-KB scope.
func TestBuildRetrieveTagScopesRejectsMultiKBBareTags(t *testing.T) {
	wire := KnowledgeRetrieveWireRequest{
		Query:           "q",
		KnowledgeBaseID: "kb-1",
		TagIDs:          []string{"tag-1"},
	}
	_, err := buildRetrieveTagScopes(wire, []string{"kb-1"})
	if err != nil {
		t.Fatalf("single-KB bare tag must be accepted: %v", err)
	}

	wireMulti := KnowledgeRetrieveWireRequest{
		Query:            "q",
		KnowledgeBaseIDs: []string{"kb-1", "kb-2"},
		TagIDs:           []string{"tag-1"},
	}
	if _, err := buildRetrieveTagScopes(wireMulti, []string{"kb-1", "kb-2"}); err == nil {
		t.Fatal("bare tag across multiple KBs must be rejected")
	}
}

// Reference: docs/knowledge-retrieve-spec.md section 2.3.
// Scoped tag mentions resolve to per-KB scopes.
func TestBuildRetrieveTagScopesFromMentions(t *testing.T) {
	wire := KnowledgeRetrieveWireRequest{
		Query:           "q",
		KnowledgeBaseID: "kb-1",
		MentionedItems: []MentionedItemRequest{
			{ID: "tag-1", Type: "tag", KBID: "kb-1"},
			{ID: "tag-2", Type: "tag", KBID: "kb-2"},
		},
	}
	scopes, err := buildRetrieveTagScopes(wire, []string{"kb-1"})
	if err != nil {
		t.Fatalf("buildRetrieveTagScopes: %v", err)
	}
	if len(scopes) == 0 {
		t.Fatal("expected at least one tag scope from mentions")
	}
	for _, scope := range scopes {
		if scope.KnowledgeBaseID == "" || len(scope.TagIDs) == 0 {
			t.Errorf("invalid scope: %#v", scope)
		}
	}
}

// Spec: docs/knowledge-retrieve-spec.md section 2.2, 2.3.
// rerank_model_id is an optional string field; it survives the Wire DTO JSON
// round-trip and flows into the service request.
func TestKnowledgeRetrieveRequestRerankModelIDJSON(t *testing.T) {
	raw := `{"query":"test","knowledge_base_id":"kb-1","rerank_model_id":"rerank-uuid"}`
	var wire KnowledgeRetrieveWireRequest
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire.RerankModelID != "rerank-uuid" {
		t.Errorf("RerankModelID = %q, want rerank-uuid", wire.RerankModelID)
	}
	svcReq := wire.toServiceRequest(nil)
	if svcReq.RerankModelID != "rerank-uuid" {
		t.Errorf("service RerankModelID = %q, want rerank-uuid", svcReq.RerankModelID)
	}
}

// Spec: docs/knowledge-retrieve-spec.md section 2.3.
// Empty rerank_model_id is omitted from JSON output.
func TestKnowledgeRetrieveRequestRerankModelIDOmitted(t *testing.T) {
	raw := `{"query":"test","knowledge_base_id":"kb-1"}`
	var wire KnowledgeRetrieveWireRequest
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire.RerankModelID != "" {
		t.Errorf("RerankModelID = %q, want empty", wire.RerankModelID)
	}
	back, err := json.Marshal(wire)
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

// Reference: docs/knowledge-retrieve-spec.md section 2.2.
// The Wire DTO flattens the request fields and converts to the service
// contract (which embeds QARequest) without data loss.
func TestKnowledgeRetrieveWireToServiceRequest(t *testing.T) {
	enabled := false
	wire := KnowledgeRetrieveWireRequest{
		Query:                 "how to login",
		KnowledgeBaseID:       "kb-1",
		KnowledgeBaseIDs:      []string{"kb-2"},
		KnowledgeIDs:          []string{"file-1"},
		EnableQueryUnderstand: &enabled,
		ChatModelID:           "model-1",
		RerankModelID:         "rerank-1",
		History:               []types.HistoryMessage{{Role: "user", Content: "hello"}},
	}
	svc := wire.toServiceRequest([]types.TagScope{{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-1"}}})
	if svc.Query != "how to login" {
		t.Errorf("Query = %q", svc.Query)
	}
	if svc.KnowledgeBaseID != "kb-1" {
		t.Errorf("KnowledgeBaseID = %q", svc.KnowledgeBaseID)
	}
	if len(svc.KnowledgeBaseIDs) != 1 || svc.KnowledgeBaseIDs[0] != "kb-2" {
		t.Errorf("KnowledgeBaseIDs = %v", svc.KnowledgeBaseIDs)
	}
	if len(svc.KnowledgeIDs) != 1 || svc.KnowledgeIDs[0] != "file-1" {
		t.Errorf("KnowledgeIDs = %v", svc.KnowledgeIDs)
	}
	if len(svc.TagScopes) != 1 || len(svc.TagScopes[0].TagIDs) != 1 || svc.TagScopes[0].TagIDs[0] != "tag-1" {
		t.Errorf("TagScopes = %v", svc.TagScopes)
	}
	if svc.EnableQueryUnderstand == nil || *svc.EnableQueryUnderstand {
		t.Errorf("EnableQueryUnderstand = %v, want false", svc.EnableQueryUnderstand)
	}
	if svc.ChatModelID != "model-1" {
		t.Errorf("ChatModelID = %q", svc.ChatModelID)
	}
	if svc.RerankModelID != "rerank-1" {
		t.Errorf("RerankModelID = %q", svc.RerankModelID)
	}
	if len(svc.History) != 1 || svc.History[0].Role != "user" {
		t.Errorf("History = %v", svc.History)
	}
}
