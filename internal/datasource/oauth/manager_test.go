package oauth

import (
	"context"
	"net/url"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
)

type dataSourceServiceMock struct {
	dsToReturn   *types.DataSource
	mergeCalled  bool
	mergePatch   map[string]interface{}
	mergeDataSrc string
}

func (m *dataSourceServiceMock) CreateDataSource(ctx context.Context, ds *types.DataSource) (*types.DataSource, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) GetDataSource(ctx context.Context, id string) (*types.DataSource, error) {
	return m.dsToReturn, nil
}

func (m *dataSourceServiceMock) ListDataSources(ctx context.Context, kbID string) ([]*types.DataSource, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) UpdateDataSource(ctx context.Context, ds *types.DataSource) (*types.DataSource, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) DeleteDataSource(ctx context.Context, id string) error {
	panic("not needed")
}

func (m *dataSourceServiceMock) UpdateDataSourceCredentials(ctx context.Context, id string, credentials map[string]interface{}) (*types.DataSource, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) ClearDataSourceCredentials(ctx context.Context, id string) error {
	panic("not needed")
}

func (m *dataSourceServiceMock) MergeDataSourceCredentials(ctx context.Context, id string, patch map[string]interface{}) error {
	m.mergeCalled = true
	m.mergeDataSrc = id
	m.mergePatch = patch
	return nil
}

func (m *dataSourceServiceMock) ValidateConnection(ctx context.Context, dsID string) error {
	panic("not needed")
}

func (m *dataSourceServiceMock) ValidateCredentials(ctx context.Context, connectorType string, credentials map[string]interface{}) error {
	panic("not needed")
}

func (m *dataSourceServiceMock) ListAvailableResources(ctx context.Context, dsID string, parentID string) ([]types.Resource, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) ResolveResourceAncestors(ctx context.Context, dsID string, resourceIDs []string) ([]string, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) ManualSync(ctx context.Context, dsID string) (*types.SyncLog, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) PauseDataSource(ctx context.Context, id string) error {
	panic("not needed")
}

func (m *dataSourceServiceMock) ResumeDataSource(ctx context.Context, id string) error {
	panic("not needed")
}

func (m *dataSourceServiceMock) GetSyncLogs(ctx context.Context, dsID string, limit int, offset int) ([]*types.SyncLog, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) GetSyncLog(ctx context.Context, syncLogID string) (*types.SyncLog, error) {
	panic("not needed")
}

func (m *dataSourceServiceMock) ProcessSync(ctx context.Context, task *asynq.Task) error {
	panic("not needed")
}

var _ interfaces.DataSourceService = (*dataSourceServiceMock)(nil)

func TestStartAuthorizationStoresDeploymentCanvasAppCredentials(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "canvas.test")
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.test/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "client-secret")

	dsSvc := &dataSourceServiceMock{}
	mgr := NewManager(nil, dsSvc)

	ds := &types.DataSource{
		ID:       "ds-1",
		TenantID: 42,
		Type:     types.ConnectorTypeCanvas,
	}

	authorizeURL, err := mgr.StartAuthorization(context.Background(), ds, 42, "https://frontend.example/callback", "/")
	if err != nil {
		t.Fatalf("StartAuthorization error: %v", err)
	}

	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("failed to parse authorize url: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("expected state param in authorize url, got %q", authorizeURL)
	}

	entry, ok := mgr.states.mem[state]
	if !ok {
		t.Fatalf("expected state entry stored in memory")
	}
	if entry.value.TenantID != 42 || entry.value.DataSourceID != ds.ID {
		t.Fatalf("unexpected state payload: %#v", entry.value)
	}
	if entry.value.BaseURL != "https://canvas.test" {
		t.Fatalf("expected trimmed base_url, got %q", entry.value.BaseURL)
	}
	if entry.value.ClientID != "client-id" {
		t.Fatalf("expected client_id, got %q", entry.value.ClientID)
	}
}

func TestStartAuthorizationFailsWhenCanvasOAuthIncomplete(t *testing.T) {
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.test/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "client-secret")

	dsSvc := &dataSourceServiceMock{}
	mgr := NewManager(nil, dsSvc)

	ds := &types.DataSource{
		ID:       "ds-1",
		TenantID: 42,
		Type:     types.ConnectorTypeCanvas,
	}

	_, err := mgr.StartAuthorization(context.Background(), ds, 42, "https://frontend.example/callback", "/")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestCompleteAuthorizationReturnsTenantMismatchBeforeTokenExchange(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "canvas.test")
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.test/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "client-secret")

	mismatchDS := &types.DataSource{
		ID:       "ds-1",
		TenantID: 99,
		Type:     types.ConnectorTypeCanvas,
	}
	dsSvc := &dataSourceServiceMock{dsToReturn: mismatchDS}
	mgr := NewManager(nil, dsSvc)

	origDS := &types.DataSource{
		ID:       "ds-1",
		TenantID: 42,
		Type:     types.ConnectorTypeCanvas,
	}

	authorizeURL, err := mgr.StartAuthorization(context.Background(), origDS, 42, "https://frontend.example/callback", "/")
	if err != nil {
		t.Fatalf("StartAuthorization error: %v", err)
	}
	u, _ := url.Parse(authorizeURL)
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("expected state param in authorize url")
	}

	_, err = mgr.CompleteAuthorization(context.Background(), state, "dummy-code")
	if err == nil {
		t.Fatalf("expected error")
	}
	if dsSvc.mergeCalled {
		t.Fatalf("expected MergeDataSourceCredentials to not be called on tenant mismatch")
	}
}

func TestStartAuthorizationUsesSameCanvasAppForDifferentTenants(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "canvas.test")
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.test/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "client-secret")

	dsSvc := &dataSourceServiceMock{}
	mgr := NewManager(nil, dsSvc)

	ds := &types.DataSource{
		ID:       "ds-1",
		TenantID: 1,
		Type:     types.ConnectorTypeCanvas,
	}

	authorizeURL1, err := mgr.StartAuthorization(context.Background(), ds, 1, "https://frontend.example/callback", "/")
	if err != nil {
		t.Fatalf("StartAuthorization tenant=1 error: %v", err)
	}
	u1, _ := url.Parse(authorizeURL1)
	state1 := u1.Query().Get("state")
	if state1 == "" {
		t.Fatalf("expected state1 param")
	}
	entry1 := mgr.states.mem[state1].value

	authorizeURL2, err := mgr.StartAuthorization(context.Background(), ds, 2, "https://frontend.example/callback", "/")
	if err != nil {
		t.Fatalf("StartAuthorization tenant=2 error: %v", err)
	}
	u2, _ := url.Parse(authorizeURL2)
	state2 := u2.Query().Get("state")
	if state2 == "" {
		t.Fatalf("expected state2 param")
	}
	entry2 := mgr.states.mem[state2].value

	if entry1.BaseURL != entry2.BaseURL || entry1.ClientID != entry2.ClientID {
		t.Fatalf("expected global app creds identical across tenants, got tenant1=%#v tenant2=%#v", entry1, entry2)
	}
}
