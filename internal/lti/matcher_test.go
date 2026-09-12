package lti

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func newTestMatcher(users *fakeUserCatalog) IdentityResolver {
	if users == nil {
		users = &fakeUserCatalog{byEmail: map[string]*types.User{}}
	}
	return NewMatcher(users, "users.lti.invalid")
}

func baseIdentity() *LaunchIdentity {
	return &LaunchIdentity{
		RegistrationID: 1,
		Sub:            "sub-uuid",
		DirectoryUID:   "20240001",
	}
}

func TestMatcherDirectoryHit(t *testing.T) {
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"20240001@users.lti.invalid": {ID: "weknora-user-1"},
	}}
	m := newTestMatcher(users)

	res, err := m.Resolve(context.Background(), baseIdentity())
	require.NoError(t, err)
	require.Equal(t, "weknora-user-1", res.UserID)
}

func TestMatcherDirectoryMissIsNotFound(t *testing.T) {
	_, err := newTestMatcher(nil).Resolve(context.Background(), baseIdentity())
	require.ErrorIs(t, err, ErrIdentityNotFound)
}

func TestMatcherMissingDirectoryUIDIsMisconfigured(t *testing.T) {
	id := baseIdentity()
	id.DirectoryUID = ""
	_, err := newTestMatcher(nil).Resolve(context.Background(), id)
	require.ErrorIs(t, err, ErrIdentityMisconfigured)
}

func TestMatcherEmptyStringClaimValueIsMisconfigured(t *testing.T) {
	// The custom claim key is configured and present in the token, but its
	// value is empty (e.g. a user without a SIS pseudonym); this is the same
	// deployment-shape signal as a missing claim.
	id := baseIdentity()
	id.DirectoryUID = ""
	_, err := newTestMatcher(nil).Resolve(context.Background(), id)
	require.ErrorIs(t, err, ErrIdentityMisconfigured)
}

func TestMatcherIgnoresRealEmailClaim(t *testing.T) {
	// Only the deterministic directory-domain email is consulted: a personal
	// account reachable by the email claim must not be selected.
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"student@example.com": {ID: "personal-account"},
	}}
	id := baseIdentity()
	id.Email = "student@example.com"
	_, err := newTestMatcher(users).Resolve(context.Background(), id)
	require.ErrorIs(t, err, ErrIdentityNotFound)
}

func TestMatcherDirectoryHitDespiteEmailClaim(t *testing.T) {
	// The reverse direction: even when the email claim points elsewhere, the
	// directory-key account wins. Placeholder-domain isolation holds both ways.
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"20240001@users.lti.invalid": {ID: "sis-account"},
		"student@example.com":        {ID: "personal-account"},
	}}
	id := baseIdentity()
	id.Email = "student@example.com"
	res, err := newTestMatcher(users).Resolve(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "sis-account", res.UserID)
}

func TestMatcherPropagatesLookupError(t *testing.T) {
	us := &fakeUserCatalog{getErr: errors.New("db down")}
	_, err := newTestMatcher(us).Resolve(context.Background(), baseIdentity())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrIdentityNotFound)
	require.NotErrorIs(t, err, ErrIdentityMisconfigured)
}

func TestMatcherMissingSubIsError(t *testing.T) {
	_, err := newTestMatcher(nil).Resolve(context.Background(), &LaunchIdentity{})
	require.Error(t, err)
}

func TestMatcherEmptyPlaceholderDomainFallsBackToDefault(t *testing.T) {
	// A hand-constructed domain of "" must not produce bare "<uid>@" lookups:
	// it falls back to the shared reserved-domain default.
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"20240001@users.lti.invalid": {ID: "weknora-user-1"},
	}}
	resolver := NewMatcher(users, "")
	res, err := resolver.Resolve(context.Background(), baseIdentity())
	require.NoError(t, err)
	require.Equal(t, "weknora-user-1", res.UserID)
}
