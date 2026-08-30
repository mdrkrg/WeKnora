package lti

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// stubUserService overrides only the methods UserCatalog forwards; the embedded
// interface satisfies the adapter's lazy type assertion.
type stubUserService struct {
	interfaces.UserService
	byEmail  map[string]*types.User
	byID     map[string]*types.User
	register *types.RegisterRequest
}

func (s *stubUserService) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	return s.byEmail[email], nil
}

func (s *stubUserService) GetUserByID(_ context.Context, id string) (*types.User, error) {
	return s.byID[id], nil
}

func (s *stubUserService) Register(_ context.Context, req *types.RegisterRequest) (*types.User, error) {
	s.register = req
	return &types.User{ID: "new-1", Email: req.Email}, nil
}

func TestUserCatalogForwardsLookup(t *testing.T) {
	svc := &stubUserService{byEmail: map[string]*types.User{"a@b.c": {ID: "u1"}}}
	uc := NewUserCatalog(svc)

	u, err := uc.GetUserByEmail(context.Background(), "a@b.c")
	require.NoError(t, err)
	require.Equal(t, "u1", u.ID)
}

func TestUserCatalogForwardsLookupByID(t *testing.T) {
	svc := &stubUserService{byID: map[string]*types.User{"u1": {ID: "u1"}}}
	uc := NewUserCatalog(svc)

	u, err := uc.GetUserByID(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, "u1", u.ID)
}

func TestUserCatalogForwardsRegister(t *testing.T) {
	svc := &stubUserService{}
	uc := NewUserCatalog(svc)

	u, err := uc.Register(context.Background(), &types.RegisterRequest{Email: "x@y.z", Username: "x"})
	require.NoError(t, err)
	require.Equal(t, "new-1", u.ID)
	require.Equal(t, "x@y.z", svc.register.Email)
}
