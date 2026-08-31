package lti

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBindingsRegistrationLookupError(t *testing.T) {
	regs := &fakeRegistrationStore{getErr: errors.New("db down")}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{}, &fakeAuditSink{})

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestBindingsUpsertError(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{upsertErr: errors.New("db down")}
	h := testBindingsHandler(regs, ids, testCatalog("u1"), &fakeAuditSink{})

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestBindingsDeleteRegistrationLookupError(t *testing.T) {
	regs := &fakeRegistrationStore{getErr: errors.New("db down")}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{}, &fakeAuditSink{})

	w := bindingsDelete(t, h, `{"registration_id":1,"external_uid":"x"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestBindingsDeleteStoreError(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{deleteErr: errors.New("db down")}
	h := testBindingsHandler(regs, ids, &fakeUserCatalog{}, &fakeAuditSink{})

	w := bindingsDelete(t, h, `{"registration_id":1,"external_uid":"x"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestBindingsRejectsUnknownUser(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	audit := &fakeAuditSink{}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{}, audit)

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"ghost"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.JSONEq(t, `{"error":"unknown user"}`, w.Body.String())
	require.Empty(t, audit.entries)
}

func TestBindingsRejectsInactiveUser(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	audit := &fakeAuditSink{}
	catalog := &fakeUserCatalog{byID: map[string]*types.User{
		"u1": {ID: "u1", IsActive: false, TenantID: 1},
	}}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, catalog, audit)

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.JSONEq(t, `{"error":"user inactive"}`, w.Body.String())
	require.Empty(t, audit.entries)
}

func TestBindingsRejectsTenantlessUser(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	audit := &fakeAuditSink{}
	catalog := &fakeUserCatalog{byID: map[string]*types.User{
		"u1": {ID: "u1", IsActive: true, TenantID: 0},
	}}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, catalog, audit)

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.JSONEq(t, `{"error":"user has no workspace"}`, w.Body.String())
	require.Empty(t, audit.entries)
}

func TestBindingsUserLookupError(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	h := testBindingsHandler(regs, &fakeIdentityStore{}, &fakeUserCatalog{
		getErr: errors.New("db down"),
	}, &fakeAuditSink{})

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}
