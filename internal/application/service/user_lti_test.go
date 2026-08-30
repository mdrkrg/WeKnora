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
	user        *types.User
	updateCalls int
	updated     *types.User
}

func (r *ltiUserRepo) GetUserByID(context.Context, string) (*types.User, error) {
	return r.user, nil
}

func (r *ltiUserRepo) UpdateUser(_ context.Context, user *types.User) error {
	r.updateCalls++
	cp := *user
	r.updated = &cp
	return nil
}

// ltiMemberService stands in for the tenant member service: GetMembership
// resolves by tenant ID; ListByUser returns rows in given order (upstream is
// stable by join time).
type ltiMemberService struct {
	interfaces.TenantMemberService
	members []*types.TenantMember
}

func (s *ltiMemberService) GetMembership(_ context.Context, _ string, tenantID uint64) (*types.TenantMember, error) {
	for _, m := range s.members {
		if m != nil && m.TenantID == tenantID {
			return m, nil
		}
	}
	return nil, nil
}

func (s *ltiMemberService) ListByUser(context.Context, string) ([]*types.TenantMember, error) {
	return s.members, nil
}

// ltiTenantService treats every tenant as existing unless listed as missing,
// driving resolveFirstMembershipTenant's tenant-existence fallback.
type ltiTenantService struct {
	interfaces.TenantService
	missing map[uint64]bool
}

func (s *ltiTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if s.missing != nil && s.missing[id] {
		return nil, errors.New("tenant not found")
	}
	return &types.Tenant{ID: id}, nil
}

type ltiTokenRepo struct {
	interfaces.AuthTokenRepository
}

func (r *ltiTokenRepo) CreateToken(context.Context, *types.AuthToken) error { return nil }

func newLTITestService(user *types.User, members ...*types.TenantMember) *userService {
	return &userService{
		userRepo:      &ltiUserRepo{user: user},
		memberService: &ltiMemberService{members: members},
		tokenRepo:     &ltiTokenRepo{},
	}
}

func TestIssueLTITokensRejectsNonMember(t *testing.T) {
	svc := newLTITestService(&types.User{ID: "u1", TenantID: 1, Email: "a@example.com"})
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
	svc := newLTITestService(&types.User{ID: "u1", TenantID: 3, Email: "a@example.com"})
	at, _, err := svc.IssueLTITokens(context.Background(), "u1", 0, false)
	if err != nil {
		t.Fatalf("IssueLTITokens: %v", err)
	}
	if at == "" {
		t.Fatal("expected access token")
	}
}

// TestIssueLTITokensDefaultTenantFailsWhenNoTenant pins the terminal state:
// ErrNoDefaultWorkspace only when the user has no active membership at all.
func TestIssueLTITokensDefaultTenantFailsWhenNoTenant(t *testing.T) {
	svc := newLTITestService(&types.User{ID: "u1", TenantID: 0})
	_, _, err := svc.IssueLTITokens(context.Background(), "u1", 0, false)
	if !errors.Is(err, ErrNoDefaultWorkspace) {
		t.Fatalf("err = %v, want ErrNoDefaultWorkspace", err)
	}
}

// TestIssueLTITokensDefaultTenantSelfHealsFromActiveMembership reproduces the
// production tenantless layout (users.tenant_id = 0, only tenant_members
// written by the daemon): the default path must heal like login and persist
// the earliest membership as home.
func TestIssueLTITokensDefaultTenantSelfHealsFromActiveMembership(t *testing.T) {
	repo := &ltiUserRepo{user: &types.User{ID: "u1", TenantID: 0, Email: "a@example.com"}}
	svc := &userService{
		userRepo: repo,
		memberService: &ltiMemberService{members: []*types.TenantMember{
			{UserID: "u1", TenantID: 7, Status: types.TenantMemberStatusActive},
		}},
		tenantService: &ltiTenantService{},
		tokenRepo:     &ltiTokenRepo{},
	}

	at, rt, err := svc.IssueLTITokens(context.Background(), "u1", 0, false)
	if err != nil {
		t.Fatalf("IssueLTITokens default path: %v", err)
	}
	if at == "" || rt == "" {
		t.Fatal("expected non-empty token pair")
	}
	if repo.updateCalls != 1 {
		t.Fatalf("UpdateUser calls = %d, want 1 (self-heal must persist the resolved home)", repo.updateCalls)
	}
	if repo.updated == nil || repo.updated.TenantID != 7 {
		t.Fatalf("persisted user = %+v, want TenantID 7", repo.updated)
	}
	if repo.user.TenantID != 7 {
		t.Fatalf("in-memory TenantID = %d, want 7", repo.user.TenantID)
	}
}

// A membership whose tenant is gone must be skipped in favour of the next
// earliest active membership (persisted home = 9).
func TestIssueLTITokensDefaultTenantFallsBackPastUnavailableMembership(t *testing.T) {
	repo := &ltiUserRepo{user: &types.User{ID: "u1", TenantID: 0, Email: "a@example.com"}}
	svc := &userService{
		userRepo: repo,
		memberService: &ltiMemberService{members: []*types.TenantMember{
			{UserID: "u1", TenantID: 7, Status: types.TenantMemberStatusActive},
			{UserID: "u1", TenantID: 9, Status: types.TenantMemberStatusActive},
		}},
		tenantService: &ltiTenantService{missing: map[uint64]bool{7: true}},
		tokenRepo:     &ltiTokenRepo{},
	}

	at, _, err := svc.IssueLTITokens(context.Background(), "u1", 0, false)
	if err != nil {
		t.Fatalf("IssueLTITokens default path: %v", err)
	}
	if at == "" {
		t.Fatal("expected access token")
	}
	if repo.updateCalls != 1 {
		t.Fatalf("UpdateUser calls = %d, want 1 (only the usable membership is persisted)", repo.updateCalls)
	}
	if repo.updated == nil || repo.updated.TenantID != 9 {
		t.Fatalf("persisted user = %+v, want TenantID 9", repo.updated)
	}
}

// Memberships exist but every tenant is gone: ErrNoDefaultWorkspace, and
// nothing is persisted.
func TestIssueLTITokensDefaultTenantFailsWhenAllMembershipTenantsUnavailable(t *testing.T) {
	repo := &ltiUserRepo{user: &types.User{ID: "u1", TenantID: 0, Email: "a@example.com"}}
	svc := &userService{
		userRepo: repo,
		memberService: &ltiMemberService{members: []*types.TenantMember{
			{UserID: "u1", TenantID: 7, Status: types.TenantMemberStatusActive},
			{UserID: "u1", TenantID: 9, Status: types.TenantMemberStatusActive},
		}},
		tenantService: &ltiTenantService{missing: map[uint64]bool{7: true, 9: true}},
		tokenRepo:     &ltiTokenRepo{},
	}

	_, _, err := svc.IssueLTITokens(context.Background(), "u1", 0, false)
	if !errors.Is(err, ErrNoDefaultWorkspace) {
		t.Fatalf("err = %v, want ErrNoDefaultWorkspace", err)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("UpdateUser calls = %d, want 0 (nothing usable to persist)", repo.updateCalls)
	}
}
