package lti

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/repository"
)

// emailResolver is the minimal deployment-default resolver: it maps a launch
// to an existing account by matching the platform-signed email claim. It never
// provisions accounts and touches no binding table, so any deployment can use
// it as-is; the four-step matcher supersedes it for directory integrations.
type emailResolver struct {
	users UserCatalog
}

// NewEmailResolver builds the minimal email-match identity resolver. The
// email claim is trusted because it comes from a signature-verified id_token.
func NewEmailResolver(users UserCatalog) IdentityResolver {
	return &emailResolver{users: users}
}

func (r *emailResolver) Resolve(ctx context.Context, id *LaunchIdentity) (*IdentityResolution, error) {
	if id == nil || id.Sub == "" {
		return nil, errors.New("lti: launch identity missing sub")
	}
	if id.Email == "" {
		return nil, ErrIdentityNotFound
	}
	u, err := r.users.GetUserByEmail(ctx, id.Email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, ErrIdentityNotFound
		}
		return nil, err
	}
	if u == nil {
		return nil, ErrIdentityNotFound
	}
	return &IdentityResolution{UserID: u.ID}, nil
}
