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
	audit := &fakeAuditSink{}
	h := testBindingsHandler(regs, ids, testCatalog("weknora-u1"), audit)

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"20240001","user_id":"weknora-u1"}`)
	require.Equal(t, http.StatusOK, w.Code)

	got, err := ids.GetByAuthorityAndUID(context.Background(), "sis:https://platform.example.com", "20240001")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "weknora-u1", got.UserID)
	require.Equal(t, "sis", got.ResolvedVia)

	require.Len(t, audit.entries, 1)
	require.Equal(t, AuditActionLTIBindingPushed, audit.entries[0].Action)
	require.Equal(t, "weknora-u1", audit.entries[0].ActorUserID)
	details := auditDetails(t, audit.entries[0])
	require.Equal(t, "sis:https://platform.example.com", details["authority"])
	require.Equal(t, "20240001", details["external_uid"])
}

// auditDetails decodes an audit entry's Details payload for assertions.
func auditDetails(t *testing.T, entry *types.AuditLog) map[string]any {
	t.Helper()
	details := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(entry.Details), &details))
	return details
}

func TestBindingsDeleteDirectoryScope(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{}
	audit := &fakeAuditSink{}
	h := testBindingsHandler(regs, ids, testCatalog("weknora-u1"), audit)

	require.Equal(t, http.StatusOK,
		bindingsPost(t, h, `{"registration_id":1,"external_uid":"20240001","user_id":"weknora-u1"}`).Code)

	w := bindingsDelete(t, h, `{"registration_id":1,"external_uid":"20240001","scope":"directory"}`)
	require.Equal(t, http.StatusNoContent, w.Code)

	got, err := ids.GetByAuthorityAndUID(context.Background(), "sis:https://platform.example.com", "20240001")
	require.NoError(t, err)
	require.Nil(t, got)

	// push + delete, in that order
	require.Len(t, audit.entries, 2)
	require.Equal(t, AuditActionLTIBindingDeleted, audit.entries[1].Action)
	details := auditDetails(t, audit.entries[1])
	require.Equal(t, "sis:https://platform.example.com", details["authority"])
	require.Equal(t, "20240001", details["external_uid"])
	require.Equal(t, "directory", details["scope"])
}

func TestBindingsDeleteScopeDefaultsToDirectory(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{rows: []*ExternalIdentity{{
		UserID: "u1", Authority: "sis:https://platform.example.com", ExternalUID: "20240001",
	}}}
	h := testBindingsHandler(regs, ids, &fakeUserCatalog{}, &fakeAuditSink{})

	w := bindingsDelete(t, h, `{"registration_id":1,"external_uid":"20240001"}`)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, ids.rows)
}

func TestBindingsDeleteLaunchScope(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{rows: []*ExternalIdentity{{
		UserID: "u1", Authority: "lti:client-1", ExternalUID: "sub-1",
	}}}
	audit := &fakeAuditSink{}
	h := testBindingsHandler(regs, ids, &fakeUserCatalog{}, audit)

	w := bindingsDelete(t, h, `{"registration_id":1,"external_uid":"sub-1","scope":"launch"}`)
	require.Equal(t, http.StatusNoContent, w.Code)

	got, err := ids.GetByAuthorityAndUID(context.Background(), "lti:client-1", "sub-1")
	require.NoError(t, err)
	require.Nil(t, got)

	require.Len(t, audit.entries, 1)
	details := auditDetails(t, audit.entries[0])
	require.Equal(t, "lti:client-1", details["authority"])
	require.Equal(t, "launch", details["scope"])
}

func TestBindingsDeleteMissingRow(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	audit := &fakeAuditSink{}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{}, audit)

	w := bindingsDelete(t, h, `{"registration_id":1,"external_uid":"ghost"}`)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, audit.entries)
}

func TestBindingsDeleteInvalidScope(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{}, &fakeAuditSink{})

	require.Equal(t, http.StatusBadRequest,
		bindingsDelete(t, h, `{"registration_id":1,"external_uid":"x","scope":"other"}`).Code)
}

func TestBindingsDeleteUnknownRegistration(t *testing.T) {
	h := testBindingsHandler(&fakeRegistrationStore{}, &fakeIdentityStore{}, &fakeUserCatalog{}, &fakeAuditSink{})

	require.Equal(t, http.StatusBadRequest,
		bindingsDelete(t, h, `{"registration_id":999,"external_uid":"x"}`).Code)
}

func TestBindingsDeleteMissingFields(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{}, &fakeAuditSink{})

	require.Equal(t, http.StatusBadRequest, bindingsDelete(t, h, `{"registration_id":1}`).Code)
	require.Equal(t, http.StatusBadRequest, bindingsDelete(t, h, `{"external_uid":"x"}`).Code)
	require.Equal(t, http.StatusBadRequest, bindingsDelete(t, h, `not-json`).Code)
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
