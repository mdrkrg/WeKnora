package lti

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupIdentityStoreDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&ExternalIdentity{}))
	return db
}

func TestIdentityStoreUpsertIdempotent(t *testing.T) {
	db := setupIdentityStoreDB(t)
	store := NewIdentityStore(db)
	ctx := context.Background()

	id := &ExternalIdentity{UserID: "u1", Authority: "lti:c1", ExternalUID: "sub-1", ResolvedVia: "email"}
	require.NoError(t, store.Upsert(ctx, id))
	require.NoError(t, store.Upsert(ctx, id))

	var count int64
	require.NoError(t, db.Model(&ExternalIdentity{}).Count(&count).Error)
	require.Equal(t, int64(1), count)

	got, err := store.GetByAuthorityAndUID(ctx, "lti:c1", "sub-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "u1", got.UserID)

	miss, err := store.GetByAuthorityAndUID(ctx, "lti:c1", "nope")
	require.NoError(t, err)
	require.Nil(t, miss)
}
