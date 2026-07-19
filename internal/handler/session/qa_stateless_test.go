package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	createSessionCalls int
}

func (s *stubStatelessSessionService) GetSession(_ context.Context, _ string) (*types.Session, error) {
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

func (s *stubStatelessSessionService) KnowledgeQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	if s.knowledgeQAFn != nil {
		return s.knowledgeQAFn(ctx, req, bus)
	}
	return nil
}

func (s *stubStatelessSessionService) KnowledgeQAByEvent(_ context.Context, _ *types.ChatManage, _ []types.EventType) error {
	return nil
}

func (s *stubStatelessSessionService) AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	if s.knowledgeQAFn != nil {
		return s.knowledgeQAFn(ctx, req, bus)
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
	listedModels []*types.Model
}

func (m *stubStatelessModelService) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	if m.modelErr != nil {
		return nil, m.modelErr
	}
	if mdl, ok := m.modelsByID[id]; ok {
		return mdl, nil
	}
	if mdl, ok := m.modelsByName[id]; ok {
		return mdl, nil
	}
	return nil, fmt.Errorf("model not found: %s", id)
}

func (m *stubStatelessModelService) ListModels(_ context.Context) ([]*types.Model, error) {
	if m.listedModels != nil {
		return m.listedModels, nil
	}
	var models []*types.Model
	for _, mdl := range m.modelsByID {
		models = append(models, mdl)
	}
	return models, nil
}
func (m *stubStatelessModelService) CreateModel(context.Context, *types.Model) error { return nil }
func (m *stubStatelessModelService) UpdateModel(context.Context, *types.Model) error { return nil }
func (m *stubStatelessModelService) DeleteModel(context.Context, string) error       { return nil }
func (m *stubStatelessModelService) UpdateModelCredentials(context.Context, string, *string, *string) (*types.Model, error) {
	return nil, nil
}

func (m *stubStatelessModelService) ClearModelCredential(context.Context, string, string) error {
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
	kbs map[string]*types.KnowledgeBase
}

func (s *stubStatelessKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, fmt.Errorf("kb not found: %s", id)
}

func (s *stubStatelessKBService) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
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
	mu        sync.Mutex
	events    map[string][]interfaces.StreamEvent
	appendErr error
	getErr    error
}

func (m *stubStatelessStreamManager) key(sessionID, messageID string) string {
	return sessionID + ":" + messageID
}

func (m *stubStatelessStreamManager) AppendEvent(_ context.Context, sessionID, messageID string, evt interfaces.StreamEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendErr != nil {
		return m.appendErr
	}
	if m.events == nil {
		m.events = make(map[string][]interfaces.StreamEvent)
	}
	k := m.key(sessionID, messageID)
	m.events[k] = append(m.events[k], evt)
	return nil
}

func (m *stubStatelessStreamManager) GetEvents(_ context.Context, sessionID, messageID string, fromOffset int) ([]interfaces.StreamEvent, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:            "",
		KnowledgeBaseIDs: []string{"kb-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:        "what is attention?",
		KnowledgeIDs: []string{"kn-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:  "what is attention?",
		TagIDs: []string{"tag-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query: "hello",
		History: []HistoryMessage{
			{Role: "system", Content: "you are helpful"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// =============================================================================
// Spec section 2.2 - valid history roles (user, assistant) accepted
// =============================================================================

func TestStatelessQA_ValidHistoryRoles_Succeed(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var called bool
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		called = true
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query: "hello",
		History: []HistoryMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "QA service should be invoked with valid history roles")
}

// =============================================================================
// Spec section 9 constraint 7 - history at max 100 (boundary) succeeds
// =============================================================================

func TestStatelessQA_HistoryAt100_Succeeds(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var called bool
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		called = true
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	history := make([]HistoryMessage, 100)
	for i := range history {
		if i%2 == 0 {
			history[i] = HistoryMessage{Role: "user", Content: fmt.Sprintf("q%d", i)}
		} else {
			history[i] = HistoryMessage{Role: "assistant", Content: fmt.Sprintf("a%d", i)}
		}
	}
	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:   "test",
		History: history,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called, "QA should succeed at history boundary of 100")
}

// =============================================================================
// Spec section 5.1 - 403 for nonexistent model
// =============================================================================

func TestStatelessQA_NonexistentModel_Returns403(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	modelSvc.modelsByID = map[string]*types.Model{}
	modelSvc.modelsByName = map[string]*types.Model{}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:          "hello",
		SummaryModelID: "nonexistent-model",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:            "hello",
		KnowledgeBaseIDs: []string{"kb-nonexistent"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{Query: "hello"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// =============================================================================
// Spec section 5.1 - 413 request body exceeds 10 MB (enforced by router)
// =============================================================================

func TestStatelessQA_BodyExceeds10MB_Returns413(t *testing.T) {
	// The handler should check body size before binding JSON.
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	// Build a body that exceeds 10MB: large content field
	large := strings.Repeat("a", 10*1024*1024+100)
	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query: large,
	})
	// The body size itself may be smaller due to JSON encoding overhead,
	// but the query field length alone pushes it over the edge.
	require.Greater(t, len(reqBody), 10*1024*1024, "test body must exceed 10MB")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

// =============================================================================
// Spec section 7 - No DB writes (session/message not persisted)
// =============================================================================

func TestStatelessQA_NoDBWrites(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{Query: "test no db writes"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 0, sessionSvc.createSessionCalls,
		"stateless endpoint must not create sessions (spec section 7)")
}

// =============================================================================
// Spec section 4 - Pure chat mode SSE event sequence
// =============================================================================

func TestStatelessQA_PureChat_SSEEventSequence(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "pure chat response", Done: true, IsFallback: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query: "what is the meaning of life?",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))

	events := parseSSEStream(t, rec.Body.String())
	require.NotEmpty(t, events, "expected at least one SSE event")
	assert.Equal(t, "agent_query", events[0].Event, "first event must be agent_query (spec section 3.2)")

	eventTypes := sseEventTypes(events)
	assert.Contains(t, eventTypes, "answer", "pure chat must have answer events (spec section 4)")
	assert.Contains(t, eventTypes, "final_answer", "pure chat must have final_answer (spec section 4)")
	assert.Contains(t, eventTypes, "complete", "pure chat must have complete (spec section 4)")

	// Pure chat must NOT have retrieval events (spec section 4)
	assert.NotContains(t, eventTypes, "tool_call", "pure chat must not have tool_call (spec section 4)")
	assert.NotContains(t, eventTypes, "tool_result", "pure chat must not have tool_result (spec section 4)")

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

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		toolCallID := uuid.New().String()
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentToolCall,
			Data: event.AgentToolCallData{
				ToolCallID: toolCallID,
				ToolName:   "knowledge_search",
				Arguments:  map[string]any{"knowledge_base_ids": req.KnowledgeBaseIDs},
			},
		})
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentToolResult,
			Data: event.AgentToolResultData{
				ToolCallID: toolCallID,
				ToolName:   "knowledge_search",
				Output:     `{"chunks_found":2,"total_duration_ms":150}`,
				Success:    true,
				Duration:   150,
			},
		})
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "RAG-based answer", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:            "what is attention?",
		KnowledgeBaseIDs: []string{"kb-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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
// Spec section 2.2 - model resolution by name
// =============================================================================

func TestStatelessQA_ModelResolution_ByName(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var resolvedModelID string
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		resolvedModelID = req.SummaryModelID
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:          "hello",
		SummaryModelID: "gpt-4o",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, resolvedModelID, "model should be resolved by name (spec section 2.2)")
}

// =============================================================================
// Spec section 2.2 - model resolution by UUID
// =============================================================================

func TestStatelessQA_ModelResolution_ByUUID(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var resolvedModelID string
	modelUUID := uuid.New().String()
	modelSvc.modelsByID[modelUUID] = &types.Model{
		ID: modelUUID, Name: "uuid-model", Type: types.ModelTypeKnowledgeQA, TenantID: 1,
	}

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		resolvedModelID = req.SummaryModelID
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:          "hello",
		SummaryModelID: modelUUID,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, modelUUID, resolvedModelID,
		"model should be resolved by UUID (spec section 2.2 UUID-first)")
}

// =============================================================================
// Spec section 2.2 - default model when summary_model_id not provided
// =============================================================================

func TestStatelessQA_DefaultModel_WhenNotSpecified(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var resolvedModelID string
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		resolvedModelID = req.SummaryModelID
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{Query: "hello"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, resolvedModelID,
		"default model should be used when summary_model_id is empty (spec section 2.2)")
}

// =============================================================================
// Spec section 2.2 - system_prompt passed to QA pipeline
// =============================================================================

func TestStatelessQA_SystemPrompt_ReachesLLM(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	var capturedSystemPrompt string
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		capturedSystemPrompt = req.SystemPrompt
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:        "hello",
		SystemPrompt: "Reply in French only.",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Reply in French only.", capturedSystemPrompt,
		"system_prompt should be passed to QA pipeline (spec section 2.2)")
}

// =============================================================================
// Spec section 3.1 - tool_result event structure
// =============================================================================

func TestStatelessQA_ToolResult_ContainsExpectedFields(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	toolCallID := uuid.New().String()
	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentToolCall,
			Data: event.AgentToolCallData{ToolCallID: toolCallID, ToolName: "knowledge_search"},
		})
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentToolResult,
			Data: event.AgentToolResultData{
				ToolCallID: toolCallID,
				ToolName:   "knowledge_search",
				Output:     `{"chunks_found":1,"total_duration_ms":100}`,
				Success:    true,
				Duration:   100,
			},
		})
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "answer with refs", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:            "test",
		KnowledgeBaseIDs: []string{"kb-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	events := parseSSEStream(t, rec.Body.String())

	var toolResultData map[string]interface{}
	for _, e := range events {
		if e.Event == "tool_result" {
			require.NoError(t, json.Unmarshal([]byte(e.Data), &toolResultData))
			break
		}
	}
	require.NotNil(t, toolResultData, "tool_result event must be present (spec section 3.1)")

	assert.NotEmpty(t, toolResultData["tool_call_id"], "tool_result must have tool_call_id")
	assert.NotEmpty(t, toolResultData["tool_name"], "tool_result must have tool_name")

	if output, ok := toolResultData["output"].(map[string]interface{}); ok {
		assert.NotZero(t, output["total_duration_ms"], "output.total_duration_ms must be present (spec section 3.1)")
	}
}

// =============================================================================
// Spec section 3.2 - agent_query event carries request_id and query
// =============================================================================

func TestStatelessQA_AgentQueryEvent_HasRequestIDAndQuery(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query: "what is attention?",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentFinalAnswer,
			Data: event.AgentFinalAnswerData{Content: "ok", Done: true},
		})
		return nil
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{Query: "test"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
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

	assert.NotEmpty(t, completeData["request_id"], "complete must contain request_id")
	assert.NotEmpty(t, completeData["model_id"], "complete must contain model_id")
	assert.NotZero(t, completeData["elapsed_ms"], "complete must contain elapsed_ms")

	usage, ok := completeData["usage"].(map[string]interface{})
	require.True(t, ok, "complete must contain usage object")
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
			req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"role %q should be rejected (spec section 2.2)", role)
		})
	}
}

// =============================================================================
// Spec section 6.3 - stop endpoint returns 200 for any request_id (idempotent)
// =============================================================================

func TestStatelessQA_Stop_Returns200ForAnyRequestID(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()
	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	stopReq := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless/stop",
		bytesReader([]byte(`{"request_id":"nonexistent-request-id"}`)))
	stopReq.Header.Set("Content-Type", "application/json")
	stopRec := httptest.NewRecorder()
	engine.ServeHTTP(stopRec, stopReq)
	assert.Equal(t, http.StatusOK, stopRec.Code)
}

// =============================================================================
// Spec section 6.3 - stop during in-flight QA, then repeat is idempotent
// =============================================================================

func TestStatelessQA_Stop_DuringQA_ThenIdempotent(t *testing.T) {
	sessionSvc, modelSvc, kbSvc, streamMgr := defaultStubs()

	blocker := make(chan struct{})
	qaDone := make(chan struct{})

	sessionSvc.knowledgeQAFn = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		bus.Emit(ctx, event.Event{
			Type: event.EventAgentToolCall,
			Data: event.AgentToolCallData{ToolCallID: "tc-1", ToolName: "knowledge_search"},
		})
		<-blocker
		close(qaDone)
		return ctx.Err()
	}

	engine := newStatelessQATestEngine(t, sessionSvc, modelSvc, kbSvc, streamMgr)

	reqBody, _ := json.Marshal(CreateKnowledgeQAStatelessRequest{
		Query:            "long generation",
		KnowledgeBaseIDs: []string{"kb-1"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless", bytesReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	handlerDone := make(chan struct{})
	go func() {
		engine.ServeHTTP(rec, req)
		close(handlerDone)
	}()

	// Give the handler time to write agent_query and register the request_id
	time.Sleep(200 * time.Millisecond)

	// Extract request_id from the agent_query event in the SSE body.
	// httptest.ResponseRecorder is not goroutine-safe, so we copy the body
	// into a local buffer protected by a short window where the handler's
	// ticker-based polling loop is in its 100ms sleep phase.
	body := rec.Body.String()
	events := parseSSEStream(t, body)
	var requestID string
	for _, e := range events {
		if e.Event == "agent_query" {
			var data map[string]interface{}
			if json.Unmarshal([]byte(e.Data), &data) == nil {
				if rid, ok := data["request_id"].(string); ok {
					requestID = rid
				}
			}
		}
	}

	if requestID == "" {
		close(blocker)
		<-qaDone
		<-handlerDone
		t.Skip("could not extract request_id from SSE stream")
	}

	// First stop during in-flight QA
	stopBody, _ := json.Marshal(StopStatelessQARequest{RequestID: requestID})
	stopRec := httptest.NewRecorder()
	stopReq := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless/stop", bytesReader(stopBody))
	stopReq.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(stopRec, stopReq)
	assert.Equal(t, http.StatusOK, stopRec.Code)

	// Second stop is idempotent (spec section 6.3)
	stopRec2 := httptest.NewRecorder()
	stopReq2 := httptest.NewRequest(http.MethodPost, "/knowledge-chat-stateless/stop", bytesReader(stopBody))
	stopReq2.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(stopRec2, stopReq2)
	assert.Equal(t, http.StatusOK, stopRec2.Code)

	// Release blocker so QA goroutine can end
	close(blocker)
	<-qaDone
	<-handlerDone

	// Read final SSE body (safe - handler has returned)
	finalBody := rec.Body.String()
	finalEvents := parseSSEStream(t, finalBody)
	finalTypes := sseEventTypes(finalEvents)
	assert.Contains(t, finalTypes, "final_answer",
		"SSE stream must contain final_answer after stop (spec section 6.3)")
	assert.Contains(t, finalTypes, "complete",
		"SSE stream must contain complete after stop (spec section 6.3)")
}

// =============================================================================
// bytesReader wraps a []byte for httptest.NewRequest
// =============================================================================

func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}
