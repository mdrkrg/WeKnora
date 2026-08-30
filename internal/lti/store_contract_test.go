package lti

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupStoreDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Ticket{}, &Registration{}))
	return db
}

func TestTicketStoreConsumeRoundTrip(t *testing.T) {
	db := setupStoreDB(t)
	store := NewTicketStore(db)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, &Ticket{
		TokenHash: "h1", UserID: "user-1", ContextID: "c1", ExpiresAt: time.Now().Add(time.Minute),
	}))
	got, err := store.Consume(ctx, "h1")
	require.NoError(t, err)
	require.Equal(t, "user-1", got.UserID)

	_, err = store.Consume(ctx, "h1")
	require.ErrorIs(t, err, ErrTicketConsumed)
}

func TestTicketStoreConsumeExpired(t *testing.T) {
	db := setupStoreDB(t)
	store := NewTicketStore(db)
	require.NoError(t, store.Create(context.Background(), &Ticket{
		TokenHash: "h1", UserID: "user-1", ExpiresAt: time.Now().Add(-time.Minute),
	}))
	_, err := store.Consume(context.Background(), "h1")
	require.ErrorIs(t, err, ErrTicketExpired)
}

func TestTicketStoreDeleteExpiredTombstonePhases(t *testing.T) {
	db := setupStoreDB(t)
	store := NewTicketStore(db)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, store.Create(ctx, &Ticket{
		TokenHash: "expired-unused",
		UserID:    "u",
		ExpiresAt: now.Add(-time.Minute),
	}))
	within := now.Add(-time.Hour)
	require.NoError(t, store.Create(ctx, &Ticket{
		TokenHash:  "tombstone-kept",
		UserID:     "u",
		ExpiresAt:  now.Add(time.Hour),
		ConsumedAt: &within,
	}))
	old := now.Add(-(ticketTombstoneTTL + time.Hour))
	require.NoError(t, store.Create(ctx, &Ticket{
		TokenHash:  "tombstone-purged",
		UserID:     "u",
		ExpiresAt:  now.Add(-time.Minute),
		ConsumedAt: &old,
	}))

	deleted, err := store.DeleteExpired(ctx, now)
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted, "unused expired + tombstone beyond window are swept")

	_, err = store.Consume(ctx, "tombstone-kept")
	require.ErrorIs(t, err, ErrTicketConsumed, "kept tombstone still reports replay as consumed")
}

func TestRegistrationStoreSaveAndLookup(t *testing.T) {
	db := setupStoreDB(t)
	store := NewRegistrationStore(db)
	ctx := context.Background()

	reg := &Registration{Issuer: "https://p.example.com", ClientID: "client-1", Enabled: true}
	require.NoError(t, db.Create(reg).Error)

	fetched := time.Now()
	require.NoError(t, store.SaveKeyset(ctx, reg.ID, `{"keys":[]}`, fetched))

	got, err := store.GetByID(ctx, reg.ID)
	require.NoError(t, err)
	require.Equal(t, `{"keys":[]}`, got.PublicKeyset)
	require.NotNil(t, got.KeysetFetchedAt)

	byPair, err := store.GetByIssuerAndClientID(ctx, "https://p.example.com", "client-1")
	require.NoError(t, err)
	require.Equal(t, reg.ID, byPair.ID)

	miss, err := store.GetByIssuerAndClientID(ctx, "https://p.example.com", "client-x")
	require.NoError(t, err)
	require.Nil(t, miss)
}
