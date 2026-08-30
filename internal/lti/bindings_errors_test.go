package lti

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBindingsRegistrationLookupError(t *testing.T) {
	regs := &fakeRegistrationStore{getErr: errors.New("db down")}
	h := testBindingsHandler(regs, &fakeIdentityStore{})

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestBindingsUpsertError(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	ids := &fakeIdentityStore{upsertErr: errors.New("db down")}
	h := testBindingsHandler(regs, ids)

	w := bindingsPost(t, h, `{"registration_id":1,"external_uid":"sis-1","user_id":"u1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}
