package lti

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
)

type ticketService struct {
	store TicketStore
	ttl   time.Duration
}

// NewTicketService builds the ticket service with the given expiry TTL.
func NewTicketService(store TicketStore, ttl time.Duration) TicketService {
	if ttl <= 0 {
		ttl = config.DefaultLTITicketTTL
	}
	return &ticketService{store: store, ttl: ttl}
}

func (s *ticketService) Issue(ctx context.Context, userID, contextID string, roles []string) (string, error) {
	raw, err := randomToken()
	if err != nil {
		return "", err
	}
	rolesJSON := ""
	if len(roles) > 0 {
		b, err := json.Marshal(roles)
		if err != nil {
			return "", err
		}
		rolesJSON = string(b)
	}
	t := &Ticket{
		TokenHash: hashToken(raw),
		UserID:    userID,
		ContextID: contextID,
		Roles:     rolesJSON,
		ExpiresAt: time.Now().Add(s.ttl),
	}
	if err := s.store.Create(ctx, t); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *ticketService) Consume(ctx context.Context, raw string) (*Ticket, error) {
	if raw == "" {
		return nil, ErrTicketNotFound
	}
	return s.store.Consume(ctx, hashToken(raw))
}

func (s *ticketService) Restore(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	return s.store.Restore(ctx, hashToken(raw))
}

func (s *ticketService) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.store.DeleteExpired(ctx, cutoff)
}

// randomBase64URLBytes returns n cryptographically random bytes,
// base64url-encoded, for use as unguessable tokens (tickets, KIDs, nonces).
func randomBase64URLBytes(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// randomToken returns 32 cryptographically random bytes, base64url-encoded.
func randomToken() (string, error) {
	return randomBase64URLBytes(32)
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
