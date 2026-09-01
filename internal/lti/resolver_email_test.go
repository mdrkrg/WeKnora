package lti

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEmailResolverMatchesExistingUser(t *testing.T) {
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"student@example.com": {ID: "weknora-user-1", Email: "student@example.com"},
	}}
	r := NewEmailResolver(users)

	res, err := r.Resolve(context.Background(), &LaunchIdentity{
		Sub:   "platform-sub-1",
		Email: "student@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, "weknora-user-1", res.UserID)
}

func TestEmailResolverNotFound(t *testing.T) {
	users := &fakeUserCatalog{byEmail: map[string]*types.User{}}
	r := NewEmailResolver(users)

	_, err := r.Resolve(context.Background(), &LaunchIdentity{
		Sub:   "platform-sub-1",
		Email: "unknown@example.com",
	})
	require.ErrorIs(t, err, ErrIdentityNotFound)
}

func TestEmailResolverMissingEmail(t *testing.T) {
	r := NewEmailResolver(&fakeUserCatalog{})
	_, err := r.Resolve(context.Background(), &LaunchIdentity{Sub: "platform-sub-1"})
	require.ErrorIs(t, err, ErrIdentityNotFound)
}

func TestEmailResolverPropagatesLookupError(t *testing.T) {
	users := &fakeUserCatalog{getErr: repository.ErrUserNotFound}
	r := NewEmailResolver(users)

	_, err := r.Resolve(context.Background(), &LaunchIdentity{
		Sub:   "platform-sub-1",
		Email: "student@example.com",
	})
	require.ErrorIs(t, err, ErrIdentityNotFound)
}

func TestEmailResolverNilIdentity(t *testing.T) {
	r := NewEmailResolver(&fakeUserCatalog{})
	_, err := r.Resolve(context.Background(), nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrIdentityNotFound)
}
