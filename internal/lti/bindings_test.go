package lti

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newBindingsEngine(h *BindingsHandler) *gin.Engine {
	r := gin.New()
	r.POST("/api/v1/lti/bindings", h.Create)
	r.DELETE("/api/v1/lti/bindings", h.Delete)
	return r
}

func bindingsPost(t *testing.T, h *BindingsHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lti/bindings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	newBindingsEngine(h).ServeHTTP(w, req)
	return w
}

func bindingsDelete(t *testing.T, h *BindingsHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/lti/bindings", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	newBindingsEngine(h).ServeHTTP(w, req)
	return w
}

func testBindingsHandler(
	regs RegistrationStore, ids IdentityStore, catalog UserCatalog, audit AuditSink,
) *BindingsHandler {
	return NewBindingsHandler(regs, ids, catalog, audit)
}

// testCatalog returns a catalog where userID resolves to an active account
// with a home workspace, the shape a valid bindings push target must have.
func testCatalog(userID string) *fakeUserCatalog {
	return &fakeUserCatalog{byID: map[string]*types.User{
		userID: {ID: userID, IsActive: true, TenantID: 1},
	}}
}

func TestBindingsUnknownRegistrationRejected(t *testing.T) {
	h := testBindingsHandler(&fakeRegistrationStore{}, &fakeIdentityStore{}, &fakeUserCatalog{}, &fakeAuditSink{})
	w := bindingsPost(t, h, `{"registration_id":999,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBindingsCreatesSisBinding(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{}
	h := testBindingsHandler(regs, ids, testCatalog("weknora-u1"), &fakeAuditSink{})

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"20240001","user_id":"weknora-u1"}`)
	require.Equal(t, http.StatusOK, w.Code)

	got, err := ids.GetByAuthorityAndUID(context.Background(), "sis:https://platform.example.com", "20240001")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "weknora-u1", got.UserID)
	require.Equal(t, "sis", got.ResolvedVia)
}

func TestBindingsIdempotentRetry(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{}
	h := testBindingsHandler(regs, ids, testCatalog("weknora-u1"), &fakeAuditSink{})

	body := `{"registration_id":1,"external_uid":"20240001","user_id":"weknora-u1"}`
	require.Equal(t, http.StatusOK, bindingsPost(t, h, body).Code)
	require.Equal(t, http.StatusOK, bindingsPost(t, h, body).Code)
	require.Len(t, ids.rows, 1)
}

func TestBindingsMissingFieldsRejected(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{}, &fakeAuditSink{})

	require.Equal(t, http.StatusBadRequest, bindingsPost(t, h, `{"registration_id":1,"user_id":"u1"}`).Code)
	require.Equal(t, http.StatusBadRequest, bindingsPost(t, h, `{"registration_id":1,"external_uid":"x"}`).Code)
	require.Equal(t, http.StatusBadRequest, bindingsPost(t, h, `not-json`).Code)
}

func TestBindingsRespondsWithCreatedRow(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, testCatalog("weknora-u1"), &fakeAuditSink{})

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"20240001","user_id":"weknora-u1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Authority   string `json:"authority"`
		ExternalUID string `json:"external_uid"`
		UserID      string `json:"user_id"`
		ResolvedVia string `json:"resolved_via"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "sis:https://platform.example.com", body.Authority)
	require.Equal(t, "20240001", body.ExternalUID)
	require.Equal(t, "weknora-u1", body.UserID)
	require.Equal(t, "sis", body.ResolvedVia)
}
