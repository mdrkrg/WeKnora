package lti

import (
	"context"
	"time"

	"github.com/MicahParks/keyfunc/v3"
)

// RegistrationStore persists per-platform LTI registrations and the cached
// platform public keyset.
type RegistrationStore interface {
	GetByIssuerAndClientID(ctx context.Context, issuer, clientID string) (*Registration, error)
	GetByID(ctx context.Context, id uint64) (*Registration, error)
	SaveKeyset(ctx context.Context, id uint64, rawKeyset string, fetchedAt time.Time) error
}

// ToolKeyStore hands out the tool's active signing key, creating it lazily.
type ToolKeyStore interface {
	Ensure(ctx context.Context) (*ToolKey, error)
}

// TicketStore is the persistence half of the ticket lifecycle.
type TicketStore interface {
	Create(ctx context.Context, t *Ticket) error
	// Consume atomically marks a ticket consumed; it must fail with
	// ErrTicketNotFound/ErrTicketExpired/ErrTicketConsumed as appropriate.
	Consume(ctx context.Context, tokenHash string) (*Ticket, error)
	DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error)
}

// TicketService mints and redeems single-use launch tickets.
type TicketService interface {
	Issue(ctx context.Context, userID, contextID string, roles []string) (raw string, err error)
	Consume(ctx context.Context, raw string) (*Ticket, error)
	DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error)
}

// KeysetResolver provides the verification key set for a platform
// registration, caching it and refreshing on kid miss / rotation.
type KeysetResolver interface {
	Resolve(ctx context.Context, reg *Registration) (keyfunc.Keyfunc, error)
	Refresh(ctx context.Context, reg *Registration) (keyfunc.Keyfunc, error)
}

// IdentityResolver maps a verified launch identity to a WeKnora account.
type IdentityResolver interface {
	Resolve(ctx context.Context, identity *LaunchIdentity) (*IdentityResolution, error)
}

// TokenMinter mints the session JWT pair for a resolved user, either for their
// default tenant or for an explicitly targeted tenant (with membership check).
type TokenMinter interface {
	IssueDefault(ctx context.Context, userID string) (*TokenResult, error)
	IssueForTenant(ctx context.Context, userID string, tenantID uint64) (*TokenResult, error)
}
