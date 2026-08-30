package lti

import (
	"errors"
	"net/http"
	"testing"

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
