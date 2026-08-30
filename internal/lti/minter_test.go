package lti

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// minterUserService stands in for *service.userService: it implements only
// the IssueLTITokens slice the lazy assertion looks for.
type minterUserService struct {
	interfaces.UserService
	err error
}

func (s *minterUserService) IssueLTITokens(
	context.Context, string, uint64, bool,
) (string, string, error) {
	return "", "", s.err
}

func TestUserTokenMinterMapsNoDefaultWorkspace(t *testing.T) {
	m := NewUserTokenMinter(&minterUserService{err: service.ErrNoDefaultWorkspace})
	_, err := m.IssueDefault(context.Background(), "u1")
	require.ErrorIs(t, err, ErrNoWorkspace)
}

func TestUserTokenMinterMapsMembershipNotFound(t *testing.T) {
	m := NewUserTokenMinter(&minterUserService{err: service.ErrMembershipNotFound})
	_, err := m.IssueForTenant(context.Background(), "u1", 7)
	require.ErrorIs(t, err, ErrNotTenantMember)
}
