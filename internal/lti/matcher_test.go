package lti

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func newTestMatcher(
	users *fakeUserCatalog, ids *fakeIdentityStore, audit *fakeAuditSink,
) IdentityResolver {
	if users == nil {
		users = &fakeUserCatalog{byEmail: map[string]*types.User{}}
	}
	if ids == nil {
		ids = &fakeIdentityStore{}
	}
	if audit == nil {
		audit = &fakeAuditSink{}
	}
	return NewMatcher(users, ids, audit, "users.lti.invalid")
}

func baseIdentity() *LaunchIdentity {
	return &LaunchIdentity{
		RegistrationID: 1,
		ClientID:       "client-1",
		Issuer:         "https://platform.example.com",
		Sub:            "sub-uuid",
		Email:          "student@example.com",
	}
}

func TestMatcherStep1ExistingBindingWins(t *testing.T) {
	ids := &fakeIdentityStore{rows: []*ExternalIdentity{
		{Authority: "lti:client-1", ExternalUID: "sub-uuid", UserID: "bound-user", ResolvedVia: "existing"},
	}}
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"student@example.com": {ID: "email-user"},
	}}
	m := newTestMatcher(users, ids, nil)

	res, err := m.Resolve(context.Background(), baseIdentity())
	require.NoError(t, err)
	require.Equal(t, "bound-user", res.UserID)
	require.Empty(t, users.registerArgs, "step 1 must short-circuit before any creation")
}

func TestMatcherStep2DirectoryBinding(t *testing.T) {
	ids := &fakeIdentityStore{rows: []*ExternalIdentity{
		{
			Authority:   "sis:https://platform.example.com",
			ExternalUID: "20240001",
			UserID:      "sis-user",
			ResolvedVia: "sis",
		},
	}}
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"student@example.com": {ID: "email-user"},
	}}
	m := newTestMatcher(users, ids, nil)

	id := baseIdentity()
	id.DirectoryUID = "20240001"
	res, err := m.Resolve(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "sis-user", res.UserID)

	// The sub is bound to the same account for future launches (step 1 hit).
	bound, err := ids.GetByAuthorityAndUID(context.Background(), "lti:client-1", "sub-uuid")
	require.NoError(t, err)
	require.NotNil(t, bound)
	require.Equal(t, "sis-user", bound.UserID)
	require.Equal(t, "sis", bound.ResolvedVia)
}

func TestMatcherStep3EmailMatch(t *testing.T) {
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"student@example.com": {ID: "email-user"},
	}}
	m := newTestMatcher(users, &fakeIdentityStore{}, nil)

	res, err := m.Resolve(context.Background(), baseIdentity())
	require.NoError(t, err)
	require.Equal(t, "email-user", res.UserID)
	require.Empty(t, users.registerArgs, "step 3 must not create an account")

	bound, err := m.(*matcher).identities.GetByAuthorityAndUID(context.Background(), "lti:client-1", "sub-uuid")
	require.NoError(t, err)
	require.NotNil(t, bound)
	require.Equal(t, "email-user", bound.UserID)
	require.Equal(t, "email", bound.ResolvedVia)
}

func TestMatcherStep3PropagatesNonNotFoundError(t *testing.T) {
	us := &fakeUserCatalog{getErr: errors.New("db down")}
	m := newTestMatcher(us, &fakeIdentityStore{}, nil)

	_, err := m.Resolve(context.Background(), baseIdentity())
	require.Error(t, err)
	require.Empty(t, us.registerArgs, "step 4 must not run on a non-not-found lookup error")
}

func TestMatcherStep4CreatesWithClaimEmail(t *testing.T) {
	audit := &fakeAuditSink{}
	m := newTestMatcher(&fakeUserCatalog{}, &fakeIdentityStore{}, audit)

	res, err := m.Resolve(context.Background(), baseIdentity())
	require.NoError(t, err)
	require.NotEmpty(t, res.UserID)

	us := m.(*matcher).users.(*fakeUserCatalog)
	require.Len(t, us.registerArgs, 1)
	req := us.registerArgs[0]
	require.Equal(t, "student@example.com", req.Email)
	require.NotEmpty(t, req.Password)
	require.NotEqual(t, "student@example.com", req.Password)

	require.Len(t, audit.entries, 1)
	require.Equal(t, AuditActionLTIMemberProvisioned, audit.entries[0].Action)
}

func TestMatcherStep4CreatesWithPlaceholderEmail(t *testing.T) {
	us := &fakeUserCatalog{}
	m := newTestMatcher(us, &fakeIdentityStore{}, nil)

	id := baseIdentity()
	id.Email = "" // privacy level too low for email claim
	res, err := m.Resolve(context.Background(), id)
	require.NoError(t, err)
	require.NotEmpty(t, res.UserID)

	require.Len(t, us.registerArgs, 1)
	require.Equal(t, "lti-sub-uuid@users.lti.invalid", us.registerArgs[0].Email)
}

func TestMatcherStep4UniqueConflictIsAnError(t *testing.T) {
	us := &fakeUserCatalog{registerErr: errors.New("email conflict")}
	m := newTestMatcher(us, &fakeIdentityStore{}, nil)

	_, err := m.Resolve(context.Background(), baseIdentity())
	require.Error(t, err)
}

func TestMatcherStep2DoesNotOverwriteEmail(t *testing.T) {
	ids := &fakeIdentityStore{rows: []*ExternalIdentity{
		{
			Authority:   "sis:https://platform.example.com",
			ExternalUID: "20240001",
			UserID:      "sis-user",
			ResolvedVia: "sis",
		},
	}}
	us := &fakeUserCatalog{}
	m := newTestMatcher(us, ids, nil)

	id := baseIdentity()
	id.DirectoryUID = "20240001"
	res, err := m.Resolve(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "sis-user", res.UserID)
	require.Empty(t, us.registerArgs, "step 2 must never rewrite the account email")
}
