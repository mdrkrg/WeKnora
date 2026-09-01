package lti

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// userCatalogAdapter is the narrow slice of the user service the identity
// resolvers need (email match, and account registration for provisioning). It
// is satisfied by *service.userService via a lazy type assertion.
type userCatalogAdapter interface {
	GetUserByEmail(ctx context.Context, email string) (*types.User, error)
	Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error)
}

type userCatalog struct {
	us interfaces.UserService
}

// NewUserCatalog adapts the user service into the narrow UserCatalog
// contract. The interface is asserted lazily at call time, so a service that
// stops exposing the methods degrades to a per-request error instead of
// failing to boot.
func NewUserCatalog(us interfaces.UserService) UserCatalog {
	return &userCatalog{us: us}
}

func (a *userCatalog) svc() (userCatalogAdapter, error) {
	svc, ok := a.us.(userCatalogAdapter)
	if !ok {
		return nil, ErrUserServiceCapability
	}
	return svc, nil
}

func (a *userCatalog) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	svc, err := a.svc()
	if err != nil {
		return nil, err
	}
	return svc.GetUserByEmail(ctx, email)
}

func (a *userCatalog) Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error) {
	svc, err := a.svc()
	if err != nil {
		return nil, err
	}
	return svc.Register(ctx, req)
}

// NewAuditSink adapts the audit log service into the narrow AuditSink
// contract. A nil audit service degrades to a no-op sink, keeping the LTI
// package deployment-agnostic: deployments without an audit service boot and
// run, they just don't emit audit rows.
func NewAuditSink(svc interfaces.AuditLogService) AuditSink {
	if svc == nil {
		return nilSink{}
	}
	return svc
}

type nilSink struct{}

func (nilSink) Log(context.Context, *types.AuditLog) error { return nil }
