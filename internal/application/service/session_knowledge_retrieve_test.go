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
		types.CHUNK_SEARCH,         // should NOT be present after fix
		types.ENTITY_SEARCH,        // driven internally by CHUNK_SEARCH_PARALLEL
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

		// Also fire ENTITY_SEARCH so the spy tracks it.
		return next()

	case types.ENTITY_SEARCH:
		// Spy records the event; no state mutation needed.
		return nil

	case types.CHUNK_SEARCH:
		// Recorded for assertion but no-op — the real behaviour is in
		// CHUNK_SEARCH_PARALLEL.
		return nil

	case types.CHUNK_RERANK:
		chatManage.RerankResult = chatManage.SearchResult
		return nil

	case types.CHUNK_MERGE:
		chatManage.MergeResult = chatManage.RerankResult
		return nil

	case types.FILTER_TOP_K:
		// MergeResult already populated by CHUNK_MERGE; no-op.
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
		eventManager:        em,
		modelService:        &stubRetrieveModelService{models: []*types.Model{defaultModel()}},
		knowledgeBaseService: &stubRetrieveKBService{kbs: []*types.KnowledgeBase{kb}},
		tenantService:       &stubRetrieveTenantService{},
	}
}

// ---------------------------------------------------------------------------
// reference: docs/knowledge-retrieve-spec.md sections 4.1, 4.3.
// §4.1 defines the pipeline:
//   QUERY_UNDERSTAND? → CHUNK_SEARCH_PARALLEL → CHUNK_RERANK → CHUNK_MERGE → MMR_SELECT_TOP_K
// §4.3 defines Parallel Search as chunk + entity executed concurrently.
// ---------------------------------------------------------------------------

func TestRetrieveKnowledgeUsesParallelSearch(t *testing.T) {
	spy := &retrievePipelineSpy{
		quIntent:     types.IntentKBSearch,
		quRewrite:    "rewritten query",
		searchResults: []*types.SearchResult{{ID: "chunk-1", Content: "test", Score: 0.9}},
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		Query:            "original query",
		KnowledgeBaseIDs: []string{testKBID},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	events := spy.recordedEvents()
	t.Logf("triggered events: %v", eventNames(events))

	// §4.1: pipeline must include CHUNK_SEARCH_PARALLEL.
	assert.Contains(t, events, types.CHUNK_SEARCH_PARALLEL,
		"spec §4.1, §4.3: retrieve pipeline must use CHUNK_SEARCH_PARALLEL for chunk + entity search")
	// §4.3: CHUNK_SEARCH is superseded by CHUNK_SEARCH_PARALLEL.
	assert.NotContains(t, events, types.CHUNK_SEARCH,
		"spec §4.3: CHUNK_SEARCH is replaced by CHUNK_SEARCH_PARALLEL; entity search must be co-located")
}

// reference: docs/knowledge-retrieve-spec.md sections 4.2 → 4.3.
// §4.2 ③: entity extraction stores entities into internal state.
// §4.3 ②: Entity Search uses entities from the separate entity extraction call.
// Issue 8: the two stages must share the same ChatManage instance so that
// entities are visible to the search phase.
func TestRetrieveKnowledgeEntityFlowsToParallelSearch(t *testing.T) {
	spy := &retrievePipelineSpy{
		quEntity:     []string{"ProductA", "TermB"},
		quIntent:     types.IntentKBSearch,
		quRewrite:    "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		Query:            "query about ProductA and TermB",
		KnowledgeBaseIDs: []string{testKBID},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	// The ChatManage passed to CHUNK_SEARCH_PARALLEL must carry the Entity
	// injected by QUERY_UNDERSTAND (§4.2→§4.3).
	searchCM := spy.chatManageFor(types.CHUNK_SEARCH_PARALLEL)
	require.NotNil(t, searchCM, "CHUNK_SEARCH_PARALLEL was never triggered")
	assert.Equal(t, []string{"ProductA", "TermB"}, searchCM.Entity,
		"spec §4.2→§4.3: entities extracted during QUERY_UNDERSTAND must be visible to CHUNK_SEARCH_PARALLEL")
}

// reference: docs/knowledge-retrieve-spec.md §4.2 → §4.3.
// The QUERY_UNDERSTAND and CHUNK_SEARCH_PARALLEL stages must operate on
// the SAME ChatManage instance so that internal state (Entity, EntityKBIDs,
// RewriteQuery, Intent) propagates correctly.
// Fixes Issue 8: currently RetrieveKnowledge creates two separate ChatManage
// objects, causing entity state loss.
func TestRetrieveKnowledgeSingleChatManageInstance(t *testing.T) {
	spy := &retrievePipelineSpy{
		quEntity:     []string{"EntityX"},
		quIntent:     types.IntentKBSearch,
		quRewrite:    "rewritten",
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "ok"}},
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		Query:            "test",
		KnowledgeBaseIDs: []string{testKBID},
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	quCM := spy.chatManageFor(types.QUERY_UNDERSTAND)
	searchCM := spy.chatManageFor(types.CHUNK_SEARCH_PARALLEL)
	require.NotNil(t, quCM, "QUERY_UNDERSTAND was never triggered")
	require.NotNil(t, searchCM, "CHUNK_SEARCH_PARALLEL was never triggered")

	assert.Same(t, quCM, searchCM,
		"spec §4.2→§4.3: QUERY_UNDERSTAND and CHUNK_SEARCH_PARALLEL must share the same ChatManage; Issue 8: separate instances lose entity state")
}

// reference: docs/knowledge-retrieve-spec.md §4.3 skip condition (line 300).
//   "若查询理解阶段判定无需检索（意图为 chitchat / greeting / follow_up /
//    web_search / image_only / doc_only），整个并行检索跳过，results 为空数组。"
func TestRetrieveKnowledgeIntentSkipsSearch(t *testing.T) {
	spy := &retrievePipelineSpy{
		quIntent:  types.IntentChitchat,
		quRewrite: "hello",
	}
	svc := newRetrieveTestService(spy)

	req := &types.KnowledgeRetrieveRequest{
		Query:            "hello",
		KnowledgeBaseIDs: []string{testKBID},
	}
	result, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.Results, "spec §4.3: non-retrieval intent must yield empty results")
	assert.Equal(t, "hello", result.RewriteQuery)
	assert.Equal(t, types.IntentChitchat, result.Intent)

	// §4.3 跳过条件: 整个并行检索跳过.
	assert.Equal(t, 0, spy.eventCount(types.CHUNK_SEARCH),
		"spec §4.3: CHUNK_SEARCH must be skipped for non-retrieval intent")
	assert.Equal(t, 0, spy.eventCount(types.CHUNK_SEARCH_PARALLEL),
		"spec §4.3: CHUNK_SEARCH_PARALLEL must be skipped for non-retrieval intent")
}

// reference: docs/knowledge-retrieve-spec.md §4.2 (line 264).
//   "当 enable_query_understand: false 时跳过整个阶段，不调用 LLM，且不读取 history。"
func TestRetrieveKnowledgeDisabledQueryUnderstandSkipsStage(t *testing.T) {
	spy := &retrievePipelineSpy{
		searchResults: []*types.SearchResult{{ID: "c-1", Content: "result"}},
	}
	svc := newRetrieveTestService(spy)

	disabled := false
	req := &types.KnowledgeRetrieveRequest{
		Query:                 "test",
		KnowledgeBaseIDs:      []string{testKBID},
		EnableQueryUnderstand: &disabled,
	}
	_, err := svc.RetrieveKnowledge(testContext(), req)
	require.NoError(t, err)

	events := spy.recordedEvents()

	// §4.2: 当 enable_query_understand=false 时跳过整个阶段.
	assert.NotContains(t, events, types.QUERY_UNDERSTAND,
		"spec §4.2: QUERY_UNDERSTAND must not be triggered when enable_query_understand=false")
	// The search pipeline must still run (spec §3.3: intent fixed to kb_search).
	assert.Contains(t, events, types.CHUNK_SEARCH_PARALLEL,
		"spec §4.1: CHUNK_SEARCH_PARALLEL must still run when query understand is disabled")
}

// eventNames returns human-readable event type names for logging.
func eventNames(events []types.EventType) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e)
	}
	return out
}
