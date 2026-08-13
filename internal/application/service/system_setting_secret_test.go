package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// memSystemSettingRepo is an in-memory SystemSettingRepository backing the
// secret-setting tests (no DB, no Redis, no audit).
type memSystemSettingRepo struct {
	rows map[string]*types.SystemSetting
}

func newMemSystemSettingRepo() *memSystemSettingRepo {
	return &memSystemSettingRepo{rows: map[string]*types.SystemSetting{}}
}

func (r *memSystemSettingRepo) Get(_ context.Context, key string) (*types.SystemSetting, error) {
	if row, ok := r.rows[key]; ok {
		cp := *row
		return &cp, nil
	}
	return nil, nil
}

func (r *memSystemSettingRepo) List(_ context.Context) ([]*types.SystemSetting, error) {
	out := make([]*types.SystemSetting, 0, len(r.rows))
	for _, row := range r.rows {
		cp := *row
		out = append(out, &cp)
	}
	return out, nil
}

func (r *memSystemSettingRepo) Upsert(_ context.Context, s *types.SystemSetting) error {
	cp := *s
	r.rows[s.Key] = &cp
	return nil
}

func (r *memSystemSettingRepo) Delete(_ context.Context, key string) (bool, error) {
	if _, ok := r.rows[key]; !ok {
		return false, nil
	}
	delete(r.rows, key)
	return true, nil
}

func newSecretTestService(t *testing.T, repo interfaces.SystemSettingRepository) interfaces.SystemSettingService {
	t.Helper()
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	return NewSystemSettingService(repo, nil, nil, nil)
}

func storedSecretValue(t *testing.T, repo *memSystemSettingRepo, key string) string {
	t.Helper()
	row, err := repo.Get(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, row)
	var v string
	require.NoError(t, json.Unmarshal(row.Value, &v))
	return v
}

func TestSystemSetting_SecretUpdateEncryptsAtRestAndMasksReads(t *testing.T) {
	repo := newMemSystemSettingRepo()
	svc := newSecretTestService(t, repo)

	updated, err := svc.Update(context.Background(), "feishu.app_secret", "s3cr3t-app-secret")
	require.NoError(t, err)
	require.True(t, updated.IsSecret)
	require.True(t, updated.SecretConfigured)
	require.Equal(t, `""`, string(updated.Value), "Update must never return the value")

	stored := storedSecretValue(t, repo, "feishu.app_secret")
	require.True(t, strings.HasPrefix(stored, utils.EncPrefix), "secret must be encrypted at rest, got %q", stored)

	got, err := svc.Get(context.Background(), "feishu.app_secret")
	require.NoError(t, err)
	require.True(t, got.SecretConfigured)
	require.Equal(t, `""`, string(got.Value), "Get must never return the value")

	list, err := svc.List(context.Background())
	require.NoError(t, err)
	var listed *types.SystemSetting
	for _, item := range list {
		if item.Key == "feishu.app_secret" {
			listed = item
		}
	}
	require.NotNil(t, listed)
	require.True(t, listed.SecretConfigured)
	require.Equal(t, `""`, string(listed.Value), "List must never return the value")
}

func TestSystemSetting_SecretResolverDecryptsForConsumers(t *testing.T) {
	repo := newMemSystemSettingRepo()
	svc := newSecretTestService(t, repo)

	_, err := svc.Update(context.Background(), "feishu.app_secret", "s3cr3t-app-secret")
	require.NoError(t, err)

	// GetString resolves through the 3-tier ladder; the DB row wins and must
	// come back decrypted so the future Feishu connector can use it directly.
	require.Equal(t, "s3cr3t-app-secret", svc.GetString(context.Background(), "feishu.app_secret", "", "fallback"))
}

func TestSystemSetting_SecretLeaveBlankKeepsExisting(t *testing.T) {
	repo := newMemSystemSettingRepo()
	svc := newSecretTestService(t, repo)

	_, err := svc.Update(context.Background(), "feishu.app_secret", "original-secret")
	require.NoError(t, err)

	noop, err := svc.Update(context.Background(), "feishu.app_secret", "")
	require.NoError(t, err)
	require.True(t, noop.SecretConfigured)

	// The stored ciphertext must be untouched.
	require.Equal(t, "original-secret", svc.GetString(context.Background(), "feishu.app_secret", "", "fallback"))

	// Leave-blank on a fresh (never-configured) secret stays unconfigured.
	freshRepo := newMemSystemSettingRepo()
	freshSvc := newSecretTestService(t, freshRepo)
	unset, err := freshSvc.Update(context.Background(), "feishu.app_secret", "")
	require.NoError(t, err)
	require.False(t, unset.SecretConfigured)
	require.Equal(t, "fallback", freshSvc.GetString(context.Background(), "feishu.app_secret", "", "fallback"))
}

func TestSystemSetting_SecretRejectsEmptySystemAESKey(t *testing.T) {
	repo := newMemSystemSettingRepo()
	svc := newSecretTestService(t, repo)

	t.Setenv("SYSTEM_AES_KEY", "")
	_, err := svc.Update(context.Background(), "feishu.app_secret", "s3cr3t")
	require.Error(t, err, "storing a secret without SYSTEM_AES_KEY must fail loudly, never persist plaintext")
}

func TestSystemSetting_SecretUndecryptableDegradesToFallback(t *testing.T) {
	repo := newMemSystemSettingRepo()
	svc := newSecretTestService(t, repo)

	_, err := svc.Update(context.Background(), "feishu.app_secret", "s3cr3t-app-secret")
	require.NoError(t, err)
	require.Equal(t, "s3cr3t-app-secret", svc.GetString(context.Background(), "feishu.app_secret", "", "fallback"))

	t.Run("rotated key", func(t *testing.T) {
		// Simulate an operator rotating SYSTEM_AES_KEY: the stored ciphertext
		// can no longer be decrypted. The resolver must degrade to
		// env/default loudly rather than hand ciphertext to a consumer —
		// GetString would otherwise unmarshal the ciphertext string and
		// return it as the credential.
		t.Setenv("SYSTEM_AES_KEY", "abcdef0123456789abcdef0123456789")
		require.Equal(t, "fallback", svc.GetString(context.Background(), "feishu.app_secret", "", "fallback"))
	})

	t.Run("missing key", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "")
		require.Equal(t, "fallback", svc.GetString(context.Background(), "feishu.app_secret", "", "fallback"))
	})
}

func TestSystemSetting_NonSecretKeysUnaffected(t *testing.T) {
	repo := newMemSystemSettingRepo()
	svc := newSecretTestService(t, repo)

	updated, err := svc.Update(context.Background(), "feishu.app_id", "cli_abc123")
	require.NoError(t, err)
	require.False(t, updated.IsSecret)
	require.Equal(t, `"cli_abc123"`, string(updated.Value))
	require.Equal(t, "cli_abc123", svc.GetString(context.Background(), "feishu.app_id", "", ""))

	listed, err := svc.List(context.Background())
	require.NoError(t, err)
	for _, item := range listed {
		if item.Key == "feishu.app_id" {
			require.False(t, item.SecretConfigured)
			require.Equal(t, `"cli_abc123"`, string(item.Value))
		}
	}
}
