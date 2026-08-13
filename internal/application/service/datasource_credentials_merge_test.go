package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// mergeCredentialsDSRepo is a minimal DataSourceRepository whose Update
// persists the row, so MergeDataSourceCredentials merges are observable.
type mergeCredentialsDSRepo struct {
	interfaces.DataSourceRepository
	row *types.DataSource
}

func (r *mergeCredentialsDSRepo) FindByID(_ context.Context, _ string) (*types.DataSource, error) {
	return r.row, nil
}

func (r *mergeCredentialsDSRepo) Update(_ context.Context, ds *types.DataSource) error {
	cp := *ds
	r.row = &cp
	return nil
}

func newOAuthDataSource(t *testing.T, id string) *mergeCredentialsDSRepo {
	t.Helper()
	ds := &types.DataSource{ID: id, Type: "test-oauth-connector"}
	cfg := &types.DataSourceConfig{
		Type: ds.Type,
		Credentials: map[string]interface{}{
			"access_token":  "tok-1",
			"refresh_token": "ref-1",
			"scope":         "read",
		},
	}
	blob, err := cfg.ToJSON()
	require.NoError(t, err)
	ds.Config = blob
	return &mergeCredentialsDSRepo{row: ds}
}

func TestMergeDataSourceCredentials_PatchesWithoutWipingOthers(t *testing.T) {
	repo := newOAuthDataSource(t, "ds-1")
	svc := &DataSourceService{dsRepo: repo}

	err := svc.MergeDataSourceCredentials(context.Background(), "ds-1", map[string]interface{}{
		"access_token": "tok-2",
		"expires_at":   float64(1234567890),
	})
	require.NoError(t, err)

	parsed, err := repo.row.ParseConfig()
	require.NoError(t, err)
	require.Equal(t, "tok-2", parsed.Credentials["access_token"])
	require.Equal(t, "ref-1", parsed.Credentials["refresh_token"], "unrelated keys must survive the merge")
	require.Equal(t, "read", parsed.Credentials["scope"])
	require.Equal(t, float64(1234567890), parsed.Credentials["expires_at"])
}

func TestMergeDataSourceCredentials_EmptyStringDeletesKey(t *testing.T) {
	repo := newOAuthDataSource(t, "ds-1")
	svc := &DataSourceService{dsRepo: repo}

	err := svc.MergeDataSourceCredentials(context.Background(), "ds-1", map[string]interface{}{
		"refresh_token": "",
	})
	require.NoError(t, err)

	parsed, err := repo.row.ParseConfig()
	require.NoError(t, err)
	_, stillThere := parsed.Credentials["refresh_token"]
	require.False(t, stillThere, "empty-string patch must delete the key")
	require.Equal(t, "tok-1", parsed.Credentials["access_token"])
}

func TestMergeDataSourceCredentials_RejectsEmptyID(t *testing.T) {
	repo := newOAuthDataSource(t, "ds-1")
	svc := &DataSourceService{dsRepo: repo}

	err := svc.MergeDataSourceCredentials(context.Background(), "", map[string]interface{}{"access_token": "tok-2"})
	require.Error(t, err)
}
