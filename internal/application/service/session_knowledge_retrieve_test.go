package service

import (
	"context"
	"sync"
	"testing"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// pipeline spy – records triggered events and injects configurable state
// ---------------------------------------------------------------------------

type trackedEvent struct {
	eventType types.EventType
	cm        *types.ChatManage
}

type retrievePipelineSpy struct {
	mu     sync.Mutex
	events []trackedEvent

	// Configurable behaviour for QUERY_UNDERSTAND.
	quEntity  []string
	quIntent  types.QueryIntent
	quRewrite string

	// Configurable behaviour for CHUNK_SEARCH_PARALLEL.
	searchResults  []*types.SearchResult
	searchErr      error
	searchErrCount int // How many times to return the error; 0 = always.
}

func (s *retrievePipelineSpy) ActivationEvents() []types.EventType {
	return []types.EventType{
		types.QUERY_UNDERSTAND,
		types.CHUNK_SEARCH,          // should NOT be present after fix
		types.ENTITY_SEARCH,         // driven internally by CHUNK_SEARCH_PARALLEL
		types.CHUNK_SEARCH_PARALLEL, // should be present after fix
		types.CHUNK_RERANK,
		types.CHUNK_MERGE,
		types.FILTER_TOP_K,
	}
}

func (s *retrievePipelineSpy) OnEvent(_ context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *chatpipeline.PluginError,
) *chatpipeline.PluginError {
	s.mu.Lock()
	s.events = append(s.events, trackedEvent{eventType: eventType, cm: chatManage})
	s.mu.Unlock()

	switch eventType {
	case types.QUERY_UNDERSTAND:
		chatManage.RewriteQuery = s.quRewrite
		chatManage.Intent = s.quIntent
		chatManage.Entity = append([]string(nil), s.quEntity...)
		return nil

	case types.CHUNK_SEARCH_PARALLEL:
		// Simulate the parallel search merging chunk + entity results.
		chatManage.SearchResult = append([]*types.SearchResult{}, s.searchResults...)
		return next()

	case types.ENTITY_SEARCH:
		return nil

	case types.CHUNK_SEARCH:
		return nil

	case types.CHUNK_RERANK:
		chatManage.RerankResult = chatManage.SearchResult
		return nil

	case types.CHUNK_MERGE:
		chatManage.MergeResult = chatManage.RerankResult
		return nil

	case types.FILTER_TOP_K:
		return nil
	}
	return nil
}

// recordedEvents returns event types in trigger order (thread-safe copy).
func (s *retrievePipelineSpy) recordedEvents() []types.EventType {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.EventType, len(s.events))
	for i, e := range s.events {
		out[i] = e.eventType
	}
	return out
}

// chatManageFor returns the ChatManage pointer that was passed for the first
// occurrence of eventType, or nil.
func (s *retrievePipelineSpy) chatManageFor(eventType types.EventType) *types.ChatManage {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.eventType == eventType {
			return e.cm
		}
	}
	return nil
}

// eventCount returns how many times eventType was triggered.
func (s *retrievePipelineSpy) eventCount(eventType types.EventType) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.eventType == eventType {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// stubs
// ---------------------------------------------------------------------------

type stubRetrieveModelService struct {
	interfaces.ModelService
	models []*types.Model
}

func (m *stubRetrieveModelService) ListModels(context.Context) ([]*types.Model, error) {
	return m.models, nil
}

type stubRetrieveKBService struct {
	interfaces.KnowledgeBaseService
	kbs []*types.KnowledgeBase
}

func (k *stubRetrieveKBService) GetKnowledgeBasesByIDsOnly(_ context.Context, _ []string) ([]*types.KnowledgeBase, error) {
	return k.kbs, nil
}

type stubRetrieveTenantService struct {
	interfaces.TenantService
}

func (t *stubRetrieveTenantService) GetTenantByID(_ context.Context, _ uint64) (*types.Tenant, error) {
	return &types.Tenant{}, nil
}

// ---------------------------------------------------------------------------
// test helper – builds a sessionService wired with the spy pipeline
// ---------------------------------------------------------------------------

const testTenantID uint64 = 1
const testKBID = "kb-test-001"

func defaultModel() *types.Model {
	return &types.Model{
		ID:        "model-qw",
		Name:      "default-qa",
		Type:      types.ModelTypeKnowledgeQA,
		Status:    types.ModelStatusActive,
		IsDefault: true,
	}
}

func testContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, testTenantID)
	ctx = context.WithValue(ctx, types.SessionTenantIDContextKey, testTenantID)
	return ctx
}

func newRetrieveTestService(spy *retrievePipelineSpy) *sessionService {
	em := chatpipeline.NewEventManager()
	em.Register(spy)

	kb := &types.KnowledgeBase{
		ID:       testKBID,
		Name:     "test-kb",
		TenantID: testTenantID,
		Type:     types.KnowledgeBaseTypeDocument,
	}

	return &sessionService{
		cfg: &config.Config{
			Conversation: &config.ConversationConfig{
				EnableRewrite:        true,
				EnableQueryExpansion: true,
				MaxRounds:            5,
				EmbeddingTopK:        10,
				VectorThreshold:      0.5,
				KeywordThreshold:     0.3,
				RerankTopK:           5,
				RerankThreshold:      0.5,
			},
		},
		eventManager:         em,
		modelService:         &stubRetrieveModelService{models: []*types.Model{defaultModel()}},
		knowledgeBaseService: &stubRetrieveKBService{kbs: []*types.KnowledgeBase{kb}},
		tenantService:        &stubRetrieveTenantService{},
	}
}

// ---------------------------------------------------------------------------
// reference: docs/knowledge-retrieve-spec.md sections 4.1, 4.3.
// sec. 4.1 defines the pipeline:
//   QUERY_UNDERSTAND? -> CHUNK_SEARCH_PARALLEL -> CHUNK_RERANK -> CHUNK_MERGE -> FILTER_TOP_K
// sec. 4.3 defines Parallel Search as chunk + entity executed concurrently.
// ---------------------------------------------------------------------------

func TestRetrieveKnowledgeUsesParallelSearch(t *testing.T) {
	spy := &retrievePipelineSpy{
		quIntent:      types.IntentKBSearch,
		quRewrite:     "rewritten query",
		searchResults: []*types.SearchResult{{ID: "chunk-1", Content: "test", Score: 0.9}},
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{Query: "original query", KnowledgeBaseIDs: []string{testKBID}},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	events := spy.recordedEvents()
	t.Logf("triggered events: %v", eventNames(events))

	assert.Contains(t, events, types.CHUNK_SEARCH_PARALLEL,
		"spec sec. 4.1, sec. 4.3: retrieve pipeline must use CHUNK_SEARCH_PARALLEL for chunk + entity search")
	assert.NotContains(t, events, types.CHUNK_SEARCH,
		"spec sec. 4.3: CHUNK_SEARCH is replaced by CHUNK_SEARCH_PARALLEL; entity search must be co-located")
}

// reference: docs/knowledge-retrieve-spec.md sections 4.2 -> 4.3.
// sec. 4.2 (3): entity extraction stores entities into internal state.
// sec. 4.3 (2): Entity Search uses entities from the separate entity extraction call.
func TestRetrieveKnowledgeEntityFlowsToParallelSearch(t *testing.T) {
	spy := &retrievePipelineSpy{
		quEntity:      []string{"ProductA", "TermB"},
		quIntent:      types.IntentKBSearch,
		quRewrite:     "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{Query: "query about ProductA and TermB", KnowledgeBaseIDs: []string{testKBID}},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	searchCM := spy.chatManageFor(types.CHUNK_SEARCH_PARALLEL)
	require.NotNil(t, searchCM, "CHUNK_SEARCH_PARALLEL was never triggered")
	assert.Equal(t, []string{"ProductA", "TermB"}, searchCM.Entity,
		"spec sec. 4.2->sec. 4.3: entities extracted during QUERY_UNDERSTAND must be visible to CHUNK_SEARCH_PARALLEL")
}

// reference: docs/knowledge-retrieve-spec.md sec. 4.2 -> sec. 4.3.
// QUERY_UNDERSTAND and CHUNK_SEARCH_PARALLEL must operate on the SAME
// ChatManage instance so internal state (Entity, RewriteQuery, Intent)
// propagates correctly.
func TestRetrieveKnowledgeSingleChatManageInstance(t *testing.T) {
	spy := &retrievePipelineSpy{
		quEntity:      []string{"EntityX"},
		quIntent:      types.IntentKBSearch,
		quRewrite:     "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "ok"}},
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{Query: "test", KnowledgeBaseIDs: []string{testKBID}},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	quCM := spy.chatManageFor(types.QUERY_UNDERSTAND)
	searchCM := spy.chatManageFor(types.CHUNK_SEARCH_PARALLEL)
	require.NotNil(t, quCM, "QUERY_UNDERSTAND was never triggered")
	require.NotNil(t, searchCM, "CHUNK_SEARCH_PARALLEL was never triggered")

	assert.Same(t, quCM, searchCM,
		"spec sec. 4.2->sec. 4.3: QUERY_UNDERSTAND and CHUNK_SEARCH_PARALLEL must share the same ChatManage")
}

// reference: docs/knowledge-retrieve-spec.md sec. 4.3 skip condition.
func TestRetrieveKnowledgeIntentSkipsSearch(t *testing.T) {
	spy := &retrievePipelineSpy{
		quIntent:  types.IntentChitchat,
		quRewrite: "hello",
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{Query: "hello", KnowledgeBaseIDs: []string{testKBID}},
	}
	result, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.Results, "spec sec. 4.3: non-retrieval intent must yield empty results")
	assert.Equal(t, "hello", result.RewriteQuery)
	assert.Equal(t, types.IntentChitchat, result.Intent)

	assert.Equal(t, 0, spy.eventCount(types.CHUNK_SEARCH),
		"spec sec. 4.3: CHUNK_SEARCH must be skipped for non-retrieval intent")
	assert.Equal(t, 0, spy.eventCount(types.CHUNK_SEARCH_PARALLEL),
		"spec sec. 4.3: CHUNK_SEARCH_PARALLEL must be skipped for non-retrieval intent")
}

// reference: docs/knowledge-retrieve-spec.md sec. 4.2.
func TestRetrieveKnowledgeDisabledQueryUnderstandSkipsStage(t *testing.T) {
	spy := &retrievePipelineSpy{
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc := newRetrieveTestService(spy)

	disabled := false
	req := &types.KnowledgeRetrieveRequest{
		QARequest:             types.QARequest{Query: "test", KnowledgeBaseIDs: []string{testKBID}},
		EnableQueryUnderstand: &disabled,
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	events := spy.recordedEvents()

	assert.NotContains(t, events, types.QUERY_UNDERSTAND,
		"spec sec. 4.2: QUERY_UNDERSTAND must not be triggered when enable_query_understand=false")
	assert.Contains(t, events, types.CHUNK_SEARCH_PARALLEL,
		"spec sec. 4.1: CHUNK_SEARCH_PARALLEL must still run when query understand is disabled")
}

// reference: docs/knowledge-retrieve-spec.md sec. 4.2, sec. 5.3.
// When the global enable_rewrite config is false, the whole query
// understanding stage is skipped - even when enable_query_understand=true.
// No model resolution, no QUERY_UNDERSTAND trigger, no entity extraction.
func TestRetrieveKnowledgeGlobalRewriteDisabledSkipsStage(t *testing.T) {
	spy := &retrievePipelineSpy{
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc := newRetrieveTestService(spy)
	svc.cfg.Conversation.EnableRewrite = false

	enabled := true
	req := &types.KnowledgeRetrieveRequest{
		QARequest:             types.QARequest{Query: "test", KnowledgeBaseIDs: []string{testKBID}},
		EnableQueryUnderstand: &enabled,
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	events := spy.recordedEvents()
	assert.NotContains(t, events, types.QUERY_UNDERSTAND,
		"spec sec. 5.3: QUERY_UNDERSTAND must be skipped when global enable_rewrite=false")
	assert.Contains(t, events, types.CHUNK_SEARCH_PARALLEL,
		"spec sec. 4.1: CHUNK_SEARCH_PARALLEL must still run")
}

// reference: docs/knowledge-retrieve-spec.md sec. 2.3, sec. 4.4.
// EnableQueryExpansion defaults to true and flows into the ChatManage
// pipeline request.
func TestRetrieveKnowledgeQueryExpansionDefaultAndFlow(t *testing.T) {
	spy := &retrievePipelineSpy{
		quIntent:      types.IntentKBSearch,
		quRewrite:     "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc := newRetrieveTestService(spy)

	// Default: EnableQueryExpansion nil -> true.
	req := &types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{Query: "test", KnowledgeBaseIDs: []string{testKBID}},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)
	searchCM := spy.chatManageFor(types.CHUNK_SEARCH_PARALLEL)
	require.NotNil(t, searchCM)
	assert.True(t, searchCM.EnableQueryExpansion,
		"spec sec. 2.3: EnableQueryExpansion defaults to true when unset")

	// Explicitly disabled.
	spy2 := &retrievePipelineSpy{
		quIntent:      types.IntentKBSearch,
		quRewrite:     "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc2 := newRetrieveTestService(spy2)
	disabled := false
	req2 := &types.KnowledgeRetrieveRequest{
		QARequest:            types.QARequest{Query: "test", KnowledgeBaseIDs: []string{testKBID}},
		EnableQueryExpansion: &disabled,
	}
	_, err = svc2.RetrieveKnowledge(testContext(), req2)
	require.NoError(t, err)
	searchCM2 := spy2.chatManageFor(types.CHUNK_SEARCH_PARALLEL)
	require.NotNil(t, searchCM2)
	assert.False(t, searchCM2.EnableQueryExpansion,
		"spec sec. 2.3: EnableQueryExpansion=false must disable query expansion")
}

// reference: docs/knowledge-retrieve-spec.md sec. 4.5.
// Rerank model resolution: request override -> tenant config -> auto-detect.
// A specified-but-unmatched rerank_model_id hard-fails (403); an empty one
// with no tenant/auto-detect candidate leaves the model empty and skips
// rerank (sec. 5.3).
func TestRetrieveKnowledgeRerankResolution(t *testing.T) {
	// Case 1: specified but unmatched rerank_model_id -> 403 (hard fail).
	spy := &retrievePipelineSpy{
		quIntent:      types.IntentKBSearch,
		quRewrite:     "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc := newRetrieveTestService(spy)
	req := &types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{Query: "test", KnowledgeBaseIDs: []string{testKBID}, RerankModelID: "rerank-model-1"},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.Error(t, err, "unmatched rerank_model_id must hard-fail")

	// Case 2: empty rerank_model_id + no active rerank model -> skip rerank.
	spy2 := &retrievePipelineSpy{
		quIntent:      types.IntentKBSearch,
		quRewrite:     "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc2 := newRetrieveTestService(spy2)
	req2 := &types.KnowledgeRetrieveRequest{
		QARequest: types.QARequest{Query: "test", KnowledgeBaseIDs: []string{testKBID}},
	}
	_, err = svc2.RetrieveKnowledge(testContext(), req2)
	require.NoError(t, err)
	searchCM := spy2.chatManageFor(types.CHUNK_SEARCH_PARALLEL)
	require.NotNil(t, searchCM)
	assert.Equal(t, "", searchCM.RerankModelID,
		"no rerank model available -> skip rerank (spec sec. 4.5/sec. 5.3)")
}

func eventNames(events []types.EventType) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e)
	}
	return out
}
