package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestLoadConnectorConfig_WiresCredentialHooks(t *testing.T) {
	repo := newOAuthDataSource(t, "ds-1")
	svc := &DataSourceService{dsRepo: repo}

	config, err := svc.loadConnectorConfig(context.Background(), repo.row)
	require.NoError(t, err)
	require.NotNil(t, config)
	require.NotNil(t, config.OnCredentialsUpdated, "write-back hook must be attached")
	require.NotNil(t, config.OnCredentialsReload, "reload hook must be attached")

	// Write-back hook merges onto the persisted row without wiping
	// unrelated keys.
	require.NoError(t, config.OnCredentialsUpdated(context.Background(), map[string]interface{}{
		"access_token": "tok-3",
	}))
	parsed, err := repo.row.ParseConfig()
	require.NoError(t, err)
	require.Equal(t, "tok-3", parsed.Credentials["access_token"])
	require.Equal(t, "ref-1", parsed.Credentials["refresh_token"], "unrelated keys survive the write-back")

	// Reload hook returns the latest persisted credentials so a waiting
	// replica can reuse tokens rotated by the lock holder.
	creds, err := config.OnCredentialsReload(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tok-3", creds["access_token"])
	require.Equal(t, "ref-1", creds["refresh_token"])
}

func TestLoadConnectorConfig_RejectsInvalidConfig(t *testing.T) {
	repo := newOAuthDataSource(t, "ds-1")
	repo.row.Config = types.JSON(`{"credentials": {invalid`)
	svc := &DataSourceService{dsRepo: repo}

	_, err := svc.loadConnectorConfig(context.Background(), repo.row)
	require.ErrorIs(t, err, datasource.ErrInvalidConfig)
}
