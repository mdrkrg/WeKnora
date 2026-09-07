package lti

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// stubUserService overrides only the method UserCatalog forwards; the embedded
// interface satisfies the adapter's lazy type assertion.
type stubUserService struct {
	interfaces.UserService
	byEmail map[string]*types.User
}

func (s *stubUserService) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	return s.byEmail[email], nil
}

func TestUserCatalogForwardsLookup(t *testing.T) {
	svc := &stubUserService{byEmail: map[string]*types.User{
		"20240001@users.lti.invalid": {ID: "u1"},
	}}
	uc := NewUserCatalog(svc)

	u, err := uc.GetUserByEmail(context.Background(), "20240001@users.lti.invalid")
	require.NoError(t, err)
	require.Equal(t, "u1", u.ID)
}

func TestUserCatalogCapabilityError(t *testing.T) {
	// A service that does not expose the narrow slice degrades to a
	// per-request capability error instead of panicking.
	uc := NewUserCatalog(nil)

	_, err := uc.GetUserByEmail(context.Background(), "x@users.lti.invalid")
	require.ErrorIs(t, err, ErrUserServiceCapability)
}
