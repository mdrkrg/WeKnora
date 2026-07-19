package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Stub services for stateless QA tests
// =============================================================================

type stubStatelessSessionService struct {
	knowledgeQAFn      func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error
	createSessionFn    func(ctx context.Context, session *types.Session) (*types.Session, error)
	createSessionCalls int
	getSessionCalls    int
}

func (s *stubStatelessSessionService) GetSession(_ context.Context, _ string) (*types.Session, error) {
	s.getSessionCalls++
	return &types.Session{ID: "test-session", TenantID: 1}, nil
}

func (s *stubStatelessSessionService) GetSessionByID(_ context.Context, _ uint64, _ string) (*types.Session, error) {
	return &types.Session{ID: "test-session", TenantID: 1}, nil
}

func (s *stubStatelessSessionService) SetSessionOwnerID(_ context.Context, _ uint64, _, _ string) error {
	return nil
}

func (s *stubStatelessSessionService) CreateSession(_ context.Context, sess *types.Session) (*types.Session, error) {
	s.createSessionCalls++
	if s.createSessionFn != nil {
		return s.createSessionFn(context.Background(), sess)
	}
	return sess, nil
}

func (s *stubStatelessSessionService) UpdateSession(_ context.Context, _ *types.Session) error {
	return nil
}
func (s *stubStatelessSessionService) DeleteSession(_ context.Context, _ string) error { return nil }
func (s *stubStatelessSessionService) GetSessionsByTenant(_ context.Context) ([]*types.Session, error) {
	return nil, nil
}

func (s *stubStatelessSessionService) GetPagedSessionsByTenant(_ context.Context, _ *types.Pagination) (*types.PageResult, error) {
	return nil, nil
}
func (s *stubStatelessSessionService) DeleteAllSessions(_ context.Context) error { return nil }
func (s *stubStatelessSessionService) BatchDeleteSessions(_ context.Context, _ []string) error {
	return nil
}

func (s *stubStatelessSessionService) SetSessionPinned(_ context.Context, _ string, _ bool) (int64, error) {
	return 0, nil
}

func (s *stubStatelessSessionService) ListSessions(_ context.Context, _ *types.SessionListQuery) (*types.PageResult, error) {
	return nil, nil
}

func (s *stubStatelessSessionService) GenerateTitle(_ context.Context, _ *types.Session, _ []types.Message, _ string) (string, error) {
	return "", nil
}

func (s *stubStatelessSessionService) GenerateTitleAsync(_ context.Context, _ *types.Session, _ string, _ string, _ *event.EventBus) {
}

func (s *stubStatelessSessionService) KnowledgeQA(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
	if s.knowledgeQAFn != nil {
		return s.knowledgeQAFn(ctx, req, eventBus)
	}
	return nil
}

func (s *stubStatelessSessionService) KnowledgeQAByEvent(_ context.Context, _ *types.ChatManage, _ []types.EventType) error {
	return nil
}

func (s *stubStatelessSessionService) AgentQA(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
	if s.knowledgeQAFn != nil {
		return s.knowledgeQAFn(ctx, req, eventBus)
	}
	return nil
}

func (s *stubStatelessSessionService) SearchKnowledge(_ context.Context, _ []string, _ []string, _ []types.TagScope, _ string) ([]*types.SearchResult, error) {
	return nil, nil
}

func (s *stubStatelessSessionService) UpdateSessionLastRequestState(_ context.Context, _ string, _ *types.SessionLastRequestState) error {
	return nil
}

type stubStatelessModelService struct {
	modelsByID   map[string]*types.Model
	modelsByName map[string]*types.Model
	modelErr     error
}

func (s *stubStatelessModelService) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	if s.modelErr != nil {
		return nil, s.modelErr
	}
	if m, ok := s.modelsByID[id]; ok {
		return m, nil
	}
	if m, ok := s.modelsByName[id]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("model not found: %s", id)
}

func (s *stubStatelessModelService) ListModels(_ context.Context) ([]*types.Model, error) {
	var models []*types.Model
	for _, m := range s.modelsByID {
		models = append(models, m)
	}
	return models, nil
}
func (s *stubStatelessModelService) CreateModel(context.Context, *types.Model) error { return nil }
func (s *stubStatelessModelService) UpdateModel(context.Context, *types.Model) error { return nil }
func (s *stubStatelessModelService) DeleteModel(context.Context, string) error       { return nil }
func (s *stubStatelessModelService) UpdateModelCredentials(context.Context, string, *string, *string) (*types.Model, error) {
	return nil, nil
}

func (s *stubStatelessModelService) ClearModelCredential(context.Context, string, string) error {
	return nil
}

func (s *stubStatelessModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, nil
}

func (s *stubStatelessModelService) GetEmbeddingModelForTenant(context.Context, string, uint64) (embedding.Embedder, error) {
	return nil, nil
}

func (s *stubStatelessModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return nil, nil
}

func (s *stubStatelessModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return nil, nil
}

func (s *stubStatelessModelService) GetASRModel(_ context.Context, _ string) (asr.ASR, error) {
	return nil, nil
}

func (s *stubStatelessModelService) GetVLMModel(context.Context, string) (vlm.VLM, error) {
	return nil, nil
}

type stubStatelessKBService struct {
	kbs     map[string]*types.KnowledgeBase
	findErr error
}

func (s *stubStatelessKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, fmt.Errorf("kb not found: %s", id)
}

func (s *stubStatelessKBService) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, fmt.Errorf("kb not found: %s", id)
}

func (s *stubStatelessKBService) GetKnowledgeBasesByIDsOnly(_ context.Context, ids []string) ([]*types.KnowledgeBase, error) {
	var result []*types.KnowledgeBase
	for _, id := range ids {
		if kb, ok := s.kbs[id]; ok {
			result = append(result, kb)
		}
	}
	return result, nil
}

func (s *stubStatelessKBService) FillKnowledgeBaseCounts(_ context.Context, _ *types.KnowledgeBase) error {
	return nil
}

func (s *stubStatelessKBService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubStatelessKBService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubStatelessKBService) ListKnowledgeBasesByTenantID(_ context.Context, _ uint64) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubStatelessKBService) UpdateKnowledgeBase(_ context.Context, _ string, _ string, _ string, _ *types.KnowledgeBaseConfig) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *stubStatelessKBService) DeleteKnowledgeBase(context.Context, string) error { return nil }
func (s *stubStatelessKBService) TogglePinKnowledgeBase(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *stubStatelessKBService) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, nil
}

func (s *stubStatelessKBService) GetQueryEmbedding(_ context.Context, _ string, _ string) ([]float32, error) {
	return nil, nil
}

func (s *stubStatelessKBService) ResolveEmbeddingModelKeys(_ context.Context, _ []*types.KnowledgeBase) map[string]string {
	return nil
}

func (s *stubStatelessKBService) CopyKnowledgeBase(_ context.Context, _ string, _ string) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	return nil, nil, nil
}

func (s *stubStatelessKBService) DuplicateKnowledgeBase(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *stubStatelessKBService) GetRepository() interfaces.KnowledgeBaseRepository      { return nil }
func (s *stubStatelessKBService) ProcessKBDelete(_ context.Context, _ *asynq.Task) error { return nil }

type stubStatelessStreamManager struct {
	events    map[string][]interfaces.StreamEvent
	appendErr error
	getErr    error
}

func (m *stubStatelessStreamManager) key(sessionID, messageID string) string {
	return sessionID + ":" + messageID
}

func (m *stubStatelessStreamManager) AppendEvent(_ context.Context, sessionID, messageID string, event interfaces.StreamEvent) error {
	if m.appendErr != nil {
		return m.appendErr
	}
	if m.events == nil {
		m.events = make(map[string][]interfaces.StreamEvent)
	}
	k := m.key(sessionID, messageID)
	m.events[k] = append(m.events[k], event)
	return nil
}

func (m *stubStatelessStreamManager) GetEvents(_ context.Context, sessionID, messageID string, fromOffset int) ([]interfaces.StreamEvent, int, error) {
	if m.getErr != nil {
		return nil, 0, m.getErr
	}
	k := m.key(sessionID, messageID)
	events := m.events[k]
	if fromOffset >= len(events) {
		return nil, len(events), nil
	}
	result := events[fromOffset:]
	return result, len(events), nil
}

type stubStatelessFileService struct{}

func (s *stubStatelessFileService) CheckConnectivity(context.Context) error { return nil }
func (s *stubStatelessFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", nil
}

func (s *stubStatelessFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", nil
}

func (s *stubStatelessFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}

func (s *stubStatelessFileService) GetFileURL(context.Context, string) (string, error) {
	return "", nil
}
func (s *stubStatelessFileService) DeleteFile(context.Context, string) error { return nil }
func (s *stubStatelessFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

// =============================================================================
// Test helpers
// =============================================================================

type sseEvent struct {
	Event string
	Data  string
}

func parseSSEStream(t *testing.T, body string) []sseEvent {
	t.Helper()
	var events []sseEvent
	var current *sseEvent

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			current = &sseEvent{Event: strings.TrimPrefix(line, "event: ")}
		} else if strings.HasPrefix(line, "data: ") {
			if current == nil {
				current = &sseEvent{}
			}
			current.Data = strings.TrimPrefix(line, "data: ")
			events = append(events, *current)
			current = nil
		}
	}
	require.NoError(t, scanner.Err())
	return events
}

func sseEventTypes(events []sseEvent) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Event
	}
	return types
}

// =============================================================================
// Engine setup
// =============================================================================

func newStatelessQATestEngine(
	t *testing.T,
	sessionSvc *stubStatelessSessionService,
	modelSvc *stubStatelessModelService,
	kbSvc *stubStatelessKBService,
	streamMgr *stubStatelessStreamManager,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	h := &Handler{
		sessionService:       sessionSvc,
		modelService:         modelSvc,
		knowledgebaseService: kbSvc,
		streamManager:        streamMgr,
		fileService:          &stubStatelessFileService{},
	}

	r := gin.New()
	r.Use(middleware.ErrorHandler())

	authMW := func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 1, Name: "test"})
		ctx = context.WithValue(ctx, types.UserContextKey, &types.User{ID: "u1"})
		ctx = context.WithValue(ctx, types.UserIDContextKey, "u1")
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
		ctx = context.WithValue(ctx, types.RequestIDContextKey, "test-request-id")
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.RequestIDContextKey.String(), "test-request-id")
		c.Next()
	}

	r.POST("/knowledge-chat-stateless", authMW, h.KnowledgeQAStateless)
	r.POST("/knowledge-chat-stateless/stop", authMW, h.StopStatelessQA)

	return r
}

func defaultStubs() (*stubStatelessSessionService, *stubStatelessModelService, *stubStatelessKBService, *stubStatelessStreamManager) {
	modelID := uuid.New().String()
	sessionSvc := &stubStatelessSessionService{}
	modelSvc := &stubStatelessModelService{
		modelsByID: map[string]*types.Model{
			modelID: {ID: modelID, Name: "gpt-4o", Type: types.ModelTypeKnowledgeQA, TenantID: 1, IsDefault: true},
		},
		modelsByName: map[string]*types.Model{
			"gpt-4o": {ID: modelID, Name: "gpt-4o", Type: types.ModelTypeKnowledgeQA, TenantID: 1, IsDefault: true},
		},
	}
	kbSvc := &stubStatelessKBService{
		kbs: map[string]*types.KnowledgeBase{
			"kb-1": {ID: "kb-1", TenantID: 1, Name: "Test KB"},
		},
	}
	streamMgr := &stubStatelessStreamManager{}
	return sessionSvc, modelSvc, kbSvc, streamMgr
}

// =============================================================================
// Spec section 2.2 - Query required -> 400
// =============================================================================

func TestStatelessQA_EmptyQuery_Returns400(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"","knowledge_base_ids":["kb-1"]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// =============================================================================
// Spec section 2.2 - knowledge_ids requires knowledge_base_ids -> 400
// =============================================================================

func TestStatelessQA_KnowledgeIDsWithoutKBIDs_Returns400(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"what is attention?","knowledge_ids":["kn-1"]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// =============================================================================
// Spec section 2.2 - tag_ids requires knowledge_base_ids -> 400
// =============================================================================

func TestStatelessQA_TagIDsWithoutKBIDs_Returns400(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"what is attention?","tag_ids":["tag-1"]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// =============================================================================
// Spec section 9 constraint 7 - history max 100 messages -> 400
// =============================================================================

func TestStatelessQA_HistoryExceeds100_Returns400(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	history := make([]HistoryMessage, 101)
	for i := range history {
		if i%2 == 0 {
			history[i] = HistoryMessage{Role: "user", Content: fmt.Sprintf("q%d", i)}
		} else {
			history[i] = HistoryMessage{Role: "assistant", Content: fmt.Sprintf("a%d", i)}
		}
	}
	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:   "test query",
		History: history,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(string(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// =============================================================================
// Spec section 2.2 - history must not contain role "system" -> 400
// =============================================================================

func TestStatelessQA_SystemRoleInHistory_Returns400(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"hello","history":[{"role":"system","content":"you are helpful"}]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// =============================================================================
// Spec section 5.1 - 403 for nonexistent model
// =============================================================================

func TestStatelessQA_NonexistentModel_Returns403(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	modelSvc.modelsByID = map[string]*types.Model{}
	modelSvc.modelsByName = map[string]*types.Model{}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"hello","summary_model_id":"nonexistent-model"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// =============================================================================
// Spec section 5.1 - 403 for nonexistent KB
// =============================================================================

func TestStatelessQA_NonexistentKB_Returns403(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"hello","knowledge_base_ids":["kb-nonexistent"]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// =============================================================================
// Spec section 5.1 - 401 unauthenticated
// =============================================================================

func TestStatelessQA_Unauthenticated_Returns401(t *testing.T) {
	gin.SetMode(gin.TestMode)

	sessionSvc, _, _, _ := defaultStubs()
	h := &Handler{
		sessionService: sessionSvc,
	}

	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/knowledge-chat-stateless", h.KnowledgeQAStateless)

	rec := httptest.NewRecorder()
	body := `{"query":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// =============================================================================
// Spec section 5.1 - 413 request body exceeds 10 MB
// =============================================================================

func TestStatelessQA_BodyExceeds10MB_Returns413(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	largeContent := strings.Repeat("x", 10*1024*1024+100)
	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"query":"%s"}`, largeContent)
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

// =============================================================================
// Spec section 4 - Pure chat mode SSE event sequence
// =============================================================================

func TestStatelessQA_PureChat_SSEEventSequence(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "pure chat response", Done: true, IsFallback: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"what is the meaning of life?"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))

	events := parseSSEStream(t, rec.Body.String())
	require.NotEmpty(t, events, "expected at least one SSE event")
	assert.Equal(t, "agent_query", events[0].Event, "first event must be agent_query (spec section 3.1)")

	eventTypes := sseEventTypes(events)
	assert.Contains(t, eventTypes, "answer", "pure chat must have answer events (spec section 4)")
	assert.Contains(t, eventTypes, "final_answer", "pure chat must have final_answer (spec section 4)")
	assert.Contains(t, eventTypes, "complete", "pure chat must have complete (spec section 4)")

	for _, e := range events {
		if e.Event == "final_answer" {
			var data map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(e.Data), &data))
			assert.True(t, data["done"].(bool), "final_answer.done must be true (spec section 4)")
		}
	}
}

// =============================================================================
// Spec section 3.1 - Full RAG event sequence
// =============================================================================

func TestStatelessQA_RAG_SSEEventSequence(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		toolCallID := uuid.New().String()
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentToolCall,
			Data: event.AgentToolCallData{
				ToolCallID: toolCallID,
				ToolName:   "knowledge_search",
				Arguments:  map[string]any{"knowledge_base_ids": req.KnowledgeBaseIDs},
			},
		})
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentToolResult,
			Data: event.AgentToolResultData{
				ToolCallID: toolCallID,
				ToolName:   "knowledge_search",
				Output:     `{"chunks_found":2,"total_duration_ms":150}`,
				Success:    true,
				Duration:   150,
			},
		})
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "RAG-based answer", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"what is attention?","knowledge_base_ids":["kb-1"]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseSSEStream(t, rec.Body.String())
	require.NotEmpty(t, events)

	eventTypes := sseEventTypes(events)
	assert.Contains(t, eventTypes, "agent_query", "RAG flow must have agent_query (spec section 3.1)")
	assert.Contains(t, eventTypes, "tool_call", "RAG flow must have tool_call (spec section 3.1)")
	assert.Contains(t, eventTypes, "tool_result", "RAG flow must have tool_result (spec section 3.1)")
	assert.Contains(t, eventTypes, "answer", "RAG flow must have answer (spec section 3.1)")
	assert.Contains(t, eventTypes, "final_answer", "RAG flow must have final_answer (spec section 3.1)")
	assert.Contains(t, eventTypes, "complete", "RAG flow must have complete (spec section 3.1)")
}

// =============================================================================
// Spec section 2.2 - model resolution by name (UUID first, then name)
// =============================================================================

func TestStatelessQA_ModelResolution_ByName(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var resolvedModelID string
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		resolvedModelID = req.SummaryModelID
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"hello","summary_model_id":"gpt-4o"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, resolvedModelID, "model should be resolved by name (spec section 2.2)")
}

// =============================================================================
// Spec section 6.3 - Stop by request_id, repeat stop is idempotent
// =============================================================================

func TestStatelessQA_Stop_Idempotent(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentQuery,
			Data: event.AgentQueryData{Query: req.Query, RequestID: "req-stop-idempotent"},
		})
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "resp", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec1 := httptest.NewRecorder()
	body := `{"query":"test stop"}`
	req1 := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code)

	events := parseSSEStream(t, rec1.Body.String())
	var requestID string
	for _, e := range events {
		if e.Event == "agent_query" {
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(e.Data), &data); err == nil {
				if rid, ok := data["request_id"].(string); ok {
					requestID = rid
				}
			}
		}
	}
	if requestID == "" {
		t.Skip("request_id not found in SSE stream, skipping stop test")
	}

	stopBody, _ := json.Marshal(StopStatelessQARequest{RequestID: requestID})

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless/stop",
			strings.NewReader(string(stopBody)))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "stop #%d should return 200 (spec section 6.3)", i+1)
	}
}

// =============================================================================
// Spec section 6.3 - Stop endpoint returns 200 for any request_id (idempotent)
// =============================================================================

func TestStatelessQA_Stop_Returns200ForAnyRequestID(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	stopReq := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless/stop",
		strings.NewReader(`{"request_id":"nonexistent-request-id"}`))
	stopReq.Header.Set("Content-Type", "application/json")
	stopRec := httptest.NewRecorder()
	engine.ServeHTTP(stopRec, stopReq)
	assert.Equal(t, http.StatusOK, stopRec.Code)
}

// =============================================================================
// Spec section 2.2 - system_prompt passed to QA pipeline
// =============================================================================

func TestStatelessQA_SystemPrompt_ReachesLLM(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var capturedPrompt string
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		capturedPrompt = req.QuotedContext
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "response with custom prompt", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	body := `{"query":"hello","system_prompt":"Reply in French only."}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, capturedPrompt, "Reply in French only.",
		"system_prompt should be passed to the QA pipeline (spec section 2.2)")
}

// =============================================================================
// Spec section 3.1 - tool_result event contains references array
// =============================================================================

func TestStatelessQA_ToolResult_ContainsToolCallID(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	toolCallID := uuid.New().String()
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentToolCall,
			Data: event.AgentToolCallData{ToolCallID: toolCallID, ToolName: "knowledge_search"},
		})
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentToolResult,
			Data: event.AgentToolResultData{
				ToolCallID: toolCallID,
				ToolName:   "knowledge_search",
				Output:     `{"chunks_found":1,"total_duration_ms":100}`,
				Success:    true,
				Duration:   100,
			},
		})
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "answer with refs", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	body := `{"query":"test","knowledge_base_ids":["kb-1"]}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseSSEStream(t, rec.Body.String())

	foundRefs := false
	for _, e := range events {
		if e.Event == "tool_result" {
			var data map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(e.Data), &data))
			assert.NotEmpty(t, data["tool_call_id"], "tool_result must have tool_call_id (spec section 3.1)")
			if _, ok := data["references"]; ok {
				foundRefs = true
			}
		}
	}
	assert.True(t, foundRefs, "tool_result should contain references array (spec section 3.1)")
}

// =============================================================================
// Spec section 3.2 - agent_query event carries request_id and query
// =============================================================================

func TestStatelessQA_AgentQueryEvent_HasRequestIDAndQuery(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"what is attention?"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseSSEStream(t, rec.Body.String())
	require.NotEmpty(t, events)

	firstEvent := events[0]
	assert.Equal(t, "agent_query", firstEvent.Event, "first SSE event must be agent_query (spec section 3.2)")

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(firstEvent.Data), &data))
	assert.NotEmpty(t, data["request_id"], "agent_query must contain request_id (spec section 3.2)")
	assert.Equal(t, "what is attention?", data["query"], "agent_query must contain the query (spec section 3.2)")
}

// =============================================================================
// Spec section 3.2 - complete event carries usage and elapsed_ms
// =============================================================================

func TestStatelessQA_CompleteEvent_HasUsageAndTiming(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseSSEStream(t, rec.Body.String())

	var completeData map[string]interface{}
	for _, e := range events {
		if e.Event == "complete" {
			require.NoError(t, json.Unmarshal([]byte(e.Data), &completeData))
			break
		}
	}
	require.NotNil(t, completeData, "complete event not found in SSE stream (spec section 3.2)")

	assert.NotEmpty(t, completeData["request_id"], "complete must contain request_id (spec section 3.2)")
	assert.NotEmpty(t, completeData["model_id"], "complete must contain model_id (spec section 3.2)")
	assert.NotZero(t, completeData["elapsed_ms"], "complete must contain elapsed_ms (spec section 3.2)")

	usage, ok := completeData["usage"].(map[string]interface{})
	require.True(t, ok, "complete must contain usage object (spec section 3.2)")
	assert.Contains(t, usage, "prompt_tokens")
	assert.Contains(t, usage, "completion_tokens")
	assert.Contains(t, usage, "total_tokens")
}

// =============================================================================
// Spec section 2.2 - history field validation: role must be user or assistant
// =============================================================================

func TestStatelessQA_HistoryInvalidRole_Returns400(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	invalidRoles := []string{"system", "admin", "tool", "", "function"}

	for _, role := range invalidRoles {
		t.Run("role="+role, func(t *testing.T) {
			reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
				Query: "test",
				History: []HistoryMessage{
					{Role: role, Content: "some content"},
				},
			})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless",
				strings.NewReader(string(reqBody)))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"role %q should be rejected (spec section 2.2)", role)
		})
	}
}

// =============================================================================
// Spec section 4 - Pure chat final_answer has done=true
// =============================================================================

func TestStatelessQA_PureChat_FinalAnswerDone(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, eventBus *event.EventBus) error {
		eventBus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "no KB answer", Done: true, IsFallback: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	rec := httptest.NewRecorder()
	body := `{"query":"tell me a joke"}`
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseSSEStream(t, rec.Body.String())

	for _, e := range events {
		if e.Event == "final_answer" {
			var data map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(e.Data), &data))
			assert.True(t, data["done"].(bool), "final_answer.done should be true (spec section 4)")
		}
	}
}
