package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type ltiUserRepo struct {
	interfaces.UserRepository
	user *types.User
}

func (r *ltiUserRepo) GetUserByID(context.Context, string) (*types.User, error) {
	return r.user, nil
}

type ltiMemberService struct {
	interfaces.TenantMemberService
	member *types.TenantMember
}

func (s *ltiMemberService) GetMembership(context.Context, string, uint64) (*types.TenantMember, error) {
	return s.member, nil
}

type ltiTokenRepo struct {
	interfaces.AuthTokenRepository
}

func (r *ltiTokenRepo) CreateToken(context.Context, *types.AuthToken) error { return nil }

func newLTITestService(user *types.User, member *types.TenantMember) *userService {
	return &userService{
		userRepo:      &ltiUserRepo{user: user},
		memberService: &ltiMemberService{member: member},
		tokenRepo:     &ltiTokenRepo{},
	}
}

func TestIssueLTITokensRejectsNonMember(t *testing.T) {
	svc := newLTITestService(&types.User{ID: "u1", TenantID: 1, Email: "a@example.com"}, nil)
	_, _, err := svc.IssueLTITokens(context.Background(), "u1", 7, true)
	if !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("err = %v, want ErrMembershipNotFound", err)
	}
}

func TestIssueLTITokensRejectsSuspendedMember(t *testing.T) {
	svc := newLTITestService(
		&types.User{ID: "u1", TenantID: 1, Email: "a@example.com"},
		&types.TenantMember{UserID: "u1", TenantID: 7, Status: types.TenantMemberStatusSuspended},
	)
	_, _, err := svc.IssueLTITokens(context.Background(), "u1", 7, true)
	if !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("err = %v, want ErrMembershipNotFound", err)
	}
}

func TestIssueLTITokensMintsForActiveMember(t *testing.T) {
	svc := newLTITestService(
		&types.User{ID: "u1", TenantID: 1, Email: "a@example.com"},
		&types.TenantMember{UserID: "u1", TenantID: 7, Status: types.TenantMemberStatusActive},
	)
	at, rt, err := svc.IssueLTITokens(context.Background(), "u1", 7, true)
	if err != nil {
		t.Fatalf("IssueLTITokens: %v", err)
	}
	if at == "" || rt == "" {
		t.Fatal("expected non-empty token pair")
	}
}

func TestIssueLTITokensDefaultTenantUsesHomeTenant(t *testing.T) {
	svc := newLTITestService(&types.User{ID: "u1", TenantID: 3, Email: "a@example.com"}, nil)
	at, _, err := svc.IssueLTITokens(context.Background(), "u1", 0, false)
	if err != nil {
		t.Fatalf("IssueLTITokens: %v", err)
	}
	if at == "" {
		t.Fatal("expected access token")
	}
}

func TestIssueLTITokensDefaultTenantFailsWhenNoTenant(t *testing.T) {
	svc := newLTITestService(&types.User{ID: "u1", TenantID: 0}, nil)
	if _, _, err := svc.IssueLTITokens(context.Background(), "u1", 0, false); err == nil {
		t.Fatal("expected error for tenantless user")
	}
}
