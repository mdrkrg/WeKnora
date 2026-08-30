package lti

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// Matcher resolves a verified launch identity to a WeKnora account through
// the four-step match: existing LTI binding, directory (SIS) binding, email
// match, then account creation. It answers only "who is this user"; workspace
// authorization is judged independently by the handoff routing.
type matcher struct {
	users             UserCatalog
	identities        IdentityStore
	audit             AuditSink
	placeholderDomain string
}

// NewMatcher builds the four-step identity resolver. placeholderDomain hosts
// synthetic emails for launches without a directory/email claim (RFC 2606).
func NewMatcher(
	users UserCatalog,
	identities IdentityStore,
	audit AuditSink,
	placeholderDomain string,
) IdentityResolver {
	return &matcher{
		users:             users,
		identities:        identities,
		audit:             audit,
		placeholderDomain: placeholderDomain,
	}
}

// AuditActionLTIMemberProvisioned records an account auto-created from an LTI
// launch (step 4).
const AuditActionLTIMemberProvisioned types.AuditAction = "lti.member_provisioned"

func (m *matcher) Resolve(ctx context.Context, id *LaunchIdentity) (*IdentityResolution, error) {
	if id == nil || id.Sub == "" {
		return nil, errors.New("lti: launch identity missing sub")
	}
	ltiAuth := ltiAuthorityOf(id)
	if existing, err := m.identities.GetByAuthorityAndUID(ctx, ltiAuth, id.Sub); err != nil {
		return nil, err
	} else if existing != nil {
		_ = m.touch(ctx, existing)
		return &IdentityResolution{UserID: existing.UserID}, nil
	}

	// Step 2: a directory (SIS) identifier already bound to an account.
	if id.DirectoryUID != "" && id.Issuer != "" {
		if bound, err := m.identities.GetByAuthorityAndUID(ctx, sisAuthorityOf(id), id.DirectoryUID); err != nil {
			return nil, err
		} else if bound != nil {
			_ = m.bind(ctx, ltiAuth, id.Sub, bound.UserID, "sis")
			return &IdentityResolution{UserID: bound.UserID}, nil
		}
	}

	// Step 3: email match against an existing account.
	if id.Email != "" && !m.isPlaceholder(id.Email) {
		u, err := m.users.GetUserByEmail(ctx, id.Email)
		if err != nil && !errors.Is(err, repository.ErrUserNotFound) {
			return nil, err
		}
		if u != nil {
			_ = m.bind(ctx, ltiAuth, id.Sub, u.ID, "email")
			return &IdentityResolution{UserID: u.ID}, nil
		}
	}

	// Step 4: provision a new account.
	return m.create(ctx, id, ltiAuth)
}

func ltiAuthorityOf(id *LaunchIdentity) string {
	return "lti:" + id.ClientID
}

func sisAuthorityOf(id *LaunchIdentity) string {
	return "sis:" + id.Issuer
}

func (m *matcher) touch(ctx context.Context, id *ExternalIdentity) error {
	id.LastSeenAt = time.Now()
	return m.identities.Upsert(ctx, id)
}

// bind records (or refreshes) the sub -> account binding; failures are logged
// through the error return but never change the resolution outcome.
func (m *matcher) bind(ctx context.Context, ltiAuth, sub, userID, via string) error {
	now := time.Now()
	return m.identities.Upsert(ctx, &ExternalIdentity{
		UserID:      userID,
		Authority:   ltiAuth,
		ExternalUID: sub,
		ResolvedVia: via,
		CreatedAt:   now,
		LastSeenAt:  now,
	})
}

func (m *matcher) create(ctx context.Context, id *LaunchIdentity, ltiAuth string) (*IdentityResolution, error) {
	email := id.Email
	if email == "" || m.isPlaceholder(email) {
		email = "lti-" + id.Sub + "@" + m.placeholderDomain
	}
	password, err := randomToken()
	if err != nil {
		return nil, err
	}
	user, err := m.users.Register(ctx, &types.RegisterRequest{
		Username: sanitizeUsername(email),
		Email:    email,
		Password: password,
	})
	if err != nil {
		return nil, fmt.Errorf("lti: provision account: %w", err)
	}
	// Best-effort binding (same as steps 2/3): if it fails transiently, the
	// next launch reaches the same account via the step-3 email match.
	_ = m.bind(ctx, ltiAuth, id.Sub, user.ID, "created")
	m.auditProvision(ctx, id, user.ID, email)
	return &IdentityResolution{UserID: user.ID}, nil
}

func (m *matcher) auditProvision(ctx context.Context, id *LaunchIdentity, userID, email string) {
	if m.audit == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{
		"authority":    ltiAuthorityOf(id),
		"external_uid": id.Sub,
		"email":        email,
	})
	_ = m.audit.Log(ctx, &types.AuditLog{
		Action:       AuditActionLTIMemberProvisioned,
		TargetType:   "user",
		TargetID:     userID,
		TargetUserID: userID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      types.JSON(details),
	})
}

func (m *matcher) isPlaceholder(email string) bool {
	return strings.HasSuffix(email, "@"+m.placeholderDomain)
}

// sanitizeUsername derives a stable username from the account email local
// part, keeping only safe characters and bounding its length.
func sanitizeUsername(email string) string {
	local := email
	if at := strings.IndexByte(email, '@'); at >= 0 {
		local = email[:at]
	}
	var b strings.Builder
	for _, r := range local {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	u := strings.Trim(b.String(), "._-")
	if u == "" {
		return "lti_user"
	}
	if len(u) > 50 {
		u = u[:50]
	}
	return u
}
