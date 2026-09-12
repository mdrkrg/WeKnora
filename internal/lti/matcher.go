package lti

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
)

// matcher resolves a verified launch identity to a WeKnora account through a
// deterministic key lookup: the directory uid (SIS id) from the launch custom
// claim is encoded into the account email `<DirectoryUID>@<placeholderDomain>`
// under the reserved non-deliverable domain, and that exact email is looked
// up. There is no multi-path match, no binding table, and no account creation;
// a miss is fail-closed so the roster sweep alone decides who can log in. It
// answers only "who is this user"; workspace authorization is judged
// independently by the handoff routing.
type matcher struct {
	users             UserCatalog
	placeholderDomain string
}

// NewMatcher builds the deterministic identity resolver. placeholderDomain
// hosts the synthetic account emails the provisioning daemon also uses to
// create accounts (RFC 2606 reserved by default); the two sides must agree or
// every launch misses. An empty domain falls back to the shared default so
// hand-constructed configs do not silently look up bare "<uid>@" addresses.
func NewMatcher(users UserCatalog, placeholderDomain string) IdentityResolver {
	if placeholderDomain == "" {
		placeholderDomain = config.DefaultLTIPlaceholderDomain
	}
	return &matcher{
		users:             users,
		placeholderDomain: placeholderDomain,
	}
}

func (m *matcher) Resolve(ctx context.Context, id *LaunchIdentity) (*IdentityResolution, error) {
	if id == nil || id.Sub == "" {
		return nil, errors.New("lti: launch identity missing sub")
	}
	// Branch 1: no directory uid (claim not injected, directory_claim not
	// configured on the registration, or the user has no SIS pseudonym).
	if id.DirectoryUID == "" {
		return nil, ErrIdentityMisconfigured
	}
	// Branch 2: exact lookup of the deterministic account email. A missing
	// email means the roster sweep has not provisioned this user yet.
	u, err := m.users.GetUserByEmail(ctx, id.DirectoryUID+"@"+m.placeholderDomain)
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
