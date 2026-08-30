package lti

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupToolKeyDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("a", 32))
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&ToolKey{}))
	return db
}

func TestToolKeyEnsureCreatesAndReuses(t *testing.T) {
	db := setupToolKeyDB(t)
	store := NewToolKeyStore(db)

	k1, err := store.Ensure(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, k1.KID)

	var jwk map[string]any
	require.NoError(t, json.Unmarshal([]byte(k1.PublicJWK), &jwk))
	require.Equal(t, "RSA", jwk["kty"])
	require.Equal(t, k1.KID, jwk["kid"])
	require.NotEmpty(t, jwk["n"])
	require.Equal(t, "AQAB", jwk["e"])

	k2, err := store.Ensure(context.Background())
	require.NoError(t, err)
	require.Equal(t, k1.KID, k2.KID)

	var count int64
	require.NoError(t, db.Model(&ToolKey{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestToolKeyPrivateKeyEncryptedAtRest(t *testing.T) {
	db := setupToolKeyDB(t)
	store := NewToolKeyStore(db)

	k, err := store.Ensure(context.Background())
	require.NoError(t, err)

	// The row on disk must be AES-encrypted, not plaintext PEM.
	var raw string
	require.NoError(t, db.Raw("SELECT private_key FROM lti_tool_keys LIMIT 1").Scan(&raw).Error)
	require.True(t, strings.HasPrefix(raw, "enc:v1:"), "private key should be encrypted at rest")

	// Re-reading through gorm decrypts via AfterFind: the in-memory copy is
	// plaintext PEM and must match the published public JWK.
	var reread ToolKey
	require.NoError(t, db.First(&reread, "k_id = ?", k.KID).Error)
	require.Contains(t, reread.PrivateKey, "RSA PRIVATE KEY")
	block, _ := pem.Decode([]byte(reread.PrivateKey))
	require.NotNil(t, block)
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	require.NoError(t, err)

	var pub struct {
		N string `json:"n"`
		E string `json:"e"`
	}
	require.NoError(t, json.Unmarshal([]byte(k.PublicJWK), &pub))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(priv.N.Bytes()), pub.N)
	require.Equal(t, "AQAB", pub.E)
}
