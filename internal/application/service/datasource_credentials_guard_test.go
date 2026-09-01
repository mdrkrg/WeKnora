package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type guardDSRepo struct {
	mu    sync.Mutex
	rows  map[string]*types.DataSource
	lastUpdate string
}

func newGuardDSRepo(ds *types.DataSource) *guardDSRepo {
	return &guardDSRepo{rows: map[string]*types.DataSource{ds.ID: ds}}
}

func (r *guardDSRepo) Create(_ context.Context, ds *types.DataSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[ds.ID] = ds
	return nil
}

func (r *guardDSRepo) FindByID(_ context.Context, id string) (*types.DataSource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ds, ok := r.rows[id]
	if !ok {
		return nil, errors.New("data source not found")
	}
	return ds, nil
}

func (r *guardDSRepo) FindByKnowledgeBase(_ context.Context, _ string) ([]*types.DataSource, error) {
	return nil, nil
}

func (r *guardDSRepo) Update(_ context.Context, ds *types.DataSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[ds.ID] = ds
	r.lastUpdate = ds.ID
	return nil
}

func (r *guardDSRepo) UpdateSyncState(_ context.Context, _ *types.DataSource) error {
	return nil
}

func (r *guardDSRepo) Delete(_ context.Context, _ string) error { return nil }

func (r *guardDSRepo) FindActive(_ context.Context) ([]*types.DataSource, error) {
	return nil, nil
}

var _ interfaces.DataSourceRepository = (*guardDSRepo)(nil)

func newCredentialGuardService(ds *types.DataSource) (*DataSourceService, *guardDSRepo) {
	repo := newGuardDSRepo(ds)
	return &DataSourceService{dsRepo: repo}, repo
}

func TestOnCredentialsUpdatedSkipsPausedDataSource(t *testing.T) {
	ds := &types.DataSource{
		ID:       "ds-paused",
		Type:     types.ConnectorTypeCanvas,
		Status:   types.DataSourceStatusPaused,
		TenantID: 1,
		Config:   types.JSON(`{"type":"canvas","credentials":{"refresh_token":"r-old"}}`),
	}
	svc, repo := newCredentialGuardService(ds)

	config := &types.DataSourceConfig{Type: types.ConnectorTypeCanvas}
	svc.attachCredentialPersister(ds.ID, config)
	require.NotNil(t, config.OnCredentialsUpdated)

	err := config.OnCredentialsUpdated(context.Background(), map[string]interface{}{
		"access_token":  "a-new",
		"refresh_token": "r-new",
	})
	require.NoError(t, err)

	assert.Equal(t, "", repo.lastUpdate, "paused data source must not be persisted")
	assert.Equal(t, `{"type":"canvas","credentials":{"refresh_token":"r-old"}}`, repo.rows[ds.ID].Config.ToString())
}

func TestOnCredentialsUpdatedMergesForActiveDataSource(t *testing.T) {
	ds := &types.DataSource{
		ID:       "ds-active",
		Type:     types.ConnectorTypeCanvas,
		Status:   types.DataSourceStatusActive,
		TenantID: 1,
		Config:   types.JSON(`{"type":"canvas","credentials":{"refresh_token":"r-old"}}`),
	}
	svc, repo := newCredentialGuardService(ds)

	config := &types.DataSourceConfig{Type: types.ConnectorTypeCanvas}
	svc.attachCredentialPersister(ds.ID, config)
	require.NotNil(t, config.OnCredentialsUpdated)

	err := config.OnCredentialsUpdated(context.Background(), map[string]interface{}{
		"access_token": "a-new",
	})
	require.NoError(t, err)

	assert.Equal(t, ds.ID, repo.lastUpdate)
	parsed, err := repo.rows[ds.ID].ParseConfig()
	require.NoError(t, err)
	assert.Equal(t, "a-new", parsed.Credentials["access_token"])
	assert.Equal(t, "r-old", parsed.Credentials["refresh_token"])
}

func TestMergeDataSourceCredentialsStillWritableWhenPaused(t *testing.T) {
	ds := &types.DataSource{
		ID:       "ds-paused-revoke",
		Type:     types.ConnectorTypeCanvas,
		Status:   types.DataSourceStatusPaused,
		TenantID: 1,
		Config:   types.JSON(`{"type":"canvas","credentials":{"access_token":"a-old","refresh_token":"r-old"}}`),
	}
	svc, repo := newCredentialGuardService(ds)

	err := svc.MergeDataSourceCredentials(context.Background(), ds.ID, map[string]interface{}{
		"access_token":  "",
		"refresh_token": "",
		"expires_at":    "",
	})
	require.NoError(t, err)

	parsed, err := repo.rows[ds.ID].ParseConfig()
	require.NoError(t, err)
	assert.NotContains(t, parsed.Credentials, "access_token")
	assert.NotContains(t, parsed.Credentials, "refresh_token")

	err = svc.MergeDataSourceCredentials(context.Background(), ds.ID, map[string]interface{}{
		"access_token": "a-fresh",
	})
	require.NoError(t, err)
	parsed, err = repo.rows[ds.ID].ParseConfig()
	require.NoError(t, err)
	assert.Equal(t, "a-fresh", parsed.Credentials["access_token"])
}
