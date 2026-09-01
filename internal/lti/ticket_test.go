package lti

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTicketServiceIssue(t *testing.T) {
	store := &fakeTicketStore{}
	svc := NewTicketService(store, 120*time.Second)

	raw, err := svc.Issue(context.Background(), "user-1", "course-42", []string{"role-a", "role-b"})
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	require.Len(t, store.tickets, 1)

	got := store.tickets[0]
	sum := sha256.Sum256([]byte(raw))
	require.Equal(t, hex.EncodeToString(sum[:]), got.TokenHash)
	require.Equal(t, "user-1", got.UserID)
	require.Equal(t, "course-42", got.ContextID)
	require.Equal(t, `["role-a","role-b"]`, got.Roles)
	require.WithinDuration(t, time.Now().Add(120*time.Second), got.ExpiresAt, 2*time.Second)

	// Tokens must be unguessable: 32 random bytes base64url => 43 chars.
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
}

func TestTicketServiceIssueDifferentTokensPerCall(t *testing.T) {
	store := &fakeTicketStore{}
	svc := NewTicketService(store, 120*time.Second)

	a, err := svc.Issue(context.Background(), "user-1", "c1", nil)
	require.NoError(t, err)
	b, err := svc.Issue(context.Background(), "user-1", "c1", nil)
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestTicketServiceConsumeRoundTrip(t *testing.T) {
	store := &fakeTicketStore{}
	svc := NewTicketService(store, 120*time.Second)

	raw, err := svc.Issue(context.Background(), "user-1", "c1", nil)
	require.NoError(t, err)

	got, err := svc.Consume(context.Background(), raw)
	require.NoError(t, err)
	require.Equal(t, "user-1", got.UserID)
}

func TestTicketServiceConsumeDistinguishesErrors(t *testing.T) {
	store := &fakeTicketStore{}
	svc := NewTicketService(store, 120*time.Second)

	raw, err := svc.Issue(context.Background(), "user-1", "c1", nil)
	require.NoError(t, err)

	_, err = svc.Consume(context.Background(), raw)
	require.NoError(t, err)
	_, err = svc.Consume(context.Background(), raw)
	require.ErrorIs(t, err, ErrTicketConsumed)

	store.now = func() time.Time { return time.Now().Add(time.Hour) }
	raw2, err := svc.Issue(context.Background(), "user-2", "c2", nil)
	require.NoError(t, err)
	_, err = svc.Consume(context.Background(), raw2)
	require.ErrorIs(t, err, ErrTicketExpired)

	_, err = svc.Consume(context.Background(), "no-such-ticket")
	require.ErrorIs(t, err, ErrTicketNotFound)
}

func TestTicketServiceDeleteExpired(t *testing.T) {
	store := &fakeTicketStore{}
	svc := NewTicketService(store, 120*time.Second)

	_, err := svc.Issue(context.Background(), "user-1", "c1", nil)
	require.NoError(t, err)
	_, err = svc.Issue(context.Background(), "user-2", "c2", nil)
	require.NoError(t, err)

	deleted, err := svc.DeleteExpired(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
	require.Empty(t, store.tickets)
}

// Replay within the window must still 409; past it the sweep purges.
func TestTicketServiceDeleteExpiredKeepsTombstoneWithinWindow(t *testing.T) {
	store := &fakeTicketStore{}
	svc := NewTicketService(store, 120*time.Second)

	raw, err := svc.Issue(context.Background(), "user-1", "c1", nil)
	require.NoError(t, err)
	_, err = svc.Consume(context.Background(), raw)
	require.NoError(t, err)

	// Past expiry but within the tombstone window: retained, replay still 409.
	store.now = func() time.Time { return time.Now().Add(time.Hour) }
	deleted, err := svc.DeleteExpired(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(0), deleted)
	require.Len(t, store.tickets, 1)
	_, err = svc.Consume(context.Background(), raw)
	require.ErrorIs(t, err, ErrTicketConsumed)

	// Beyond the tombstone window: swept, replay now reads as not-found.
	store.now = func() time.Time { return time.Now().Add(ticketTombstoneTTL + time.Hour) }
	deleted, err = svc.DeleteExpired(context.Background(), time.Now().Add(ticketTombstoneTTL+time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	require.Empty(t, store.tickets)
}
