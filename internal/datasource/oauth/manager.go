package oauth

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/canvas"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// Manager drives the data-source OAuth2 authorization-code flow.
type Manager struct {
	states *StateStore
	dsSvc  interfaces.DataSourceService
}

// NewManager constructs a Manager. rdb may be nil (in-memory state).
func NewManager(
	rdb *redis.Client,
	dsSvc interfaces.DataSourceService,
) *Manager {
	return &Manager{
		states: NewStateStore(rdb),
		dsSvc:  dsSvc,
	}
}

func (m *Manager) resolveCanvasConfig(
	ctx context.Context,
	dsConfig *types.DataSourceConfig,
) (*canvas.Config, error) {
	cfg, err := canvas.ParseConfigForOAuth(dsConfig)
	if err != nil {
		return nil, err
	}

	app, err := LoadAppCredentials()
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, fmt.Errorf("canvas OAuth app is not configured in deployment env")
	}

	cfg.ApplyAppConfig(app)

	if cfg.GetBaseURL() == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("canvas OAuth app is not configured in deployment env")
	}
	if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
		return nil, err
	}
	return cfg, nil
}

// StartAuthorization builds the provider authorize URL for a data source.
func (m *Manager) StartAuthorization(
	ctx context.Context,
	ds *types.DataSource,
	tenantID uint64,
	redirectURI, frontendRedirect string,
) (string, error) {
	if ds == nil {
		return "", fmt.Errorf("data source is nil")
	}
	if ds.Type != types.ConnectorTypeCanvas {
		return "", fmt.Errorf("oauth is not supported for connector type %q", ds.Type)
	}
	cfg, err := ds.ParseConfig()
	if err != nil {
		return "", datasource.ErrInvalidConfig
	}
	if cfg == nil {
		cfg = &types.DataSourceConfig{Type: ds.Type}
	}
	canvasCfg, err := m.resolveCanvasConfig(ctx, cfg)
	if err != nil {
		return "", err
	}

	state, err := NewStateToken()
	if err != nil {
		return "", err
	}
	if frontendRedirect == "" {
		frontendRedirect = "/"
	}
	if err := m.states.Put(ctx, state, State{
		TenantID:         tenantID,
		DataSourceID:     ds.ID,
		RedirectURI:      redirectURI,
		FrontendRedirect: frontendRedirect,
		BaseURL:          canvasCfg.GetBaseURL(),
		ClientID:         canvasCfg.ClientID,
	}); err != nil {
		return "", err
	}
	return canvas.BuildAuthorizeURL(canvasCfg.GetBaseURL(), canvasCfg.ClientID, redirectURI, state), nil
}

// CompleteAuthorization exchanges the code and persists user tokens onto the data source.
func (m *Manager) CompleteAuthorization(ctx context.Context, stateToken, code string) (frontendRedirect string, err error) {
	st, err := m.states.Take(ctx, stateToken)
	if err != nil {
		return "/", err
	}
	frontendRedirect = st.FrontendRedirect
	if frontendRedirect == "" {
		frontendRedirect = "/"
	}

	// Callback is a public route (no JWT). Restore tenant from the signed
	// OAuth state so workspace-admin Canvas app credentials can be resolved.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, st.TenantID)

	ds, err := m.dsSvc.GetDataSource(ctx, st.DataSourceID)
	if err != nil || ds == nil {
		return frontendRedirect, fmt.Errorf("data source not found")
	}
	if ds.TenantID != st.TenantID {
		return frontendRedirect, fmt.Errorf("tenant mismatch")
	}

	cfg, err := ds.ParseConfig()
	if err != nil {
		return frontendRedirect, datasource.ErrInvalidConfig
	}
	if cfg == nil {
		cfg = &types.DataSourceConfig{Type: ds.Type}
	}
	canvasCfg, err := m.resolveCanvasConfig(ctx, cfg)
	if err != nil {
		return frontendRedirect, err
	}

	cli, err := canvas.NewClient(canvasCfg, nil)
	if err != nil {
		return frontendRedirect, err
	}
	if err := cli.ExchangeCode(ctx, code, st.RedirectURI); err != nil {
		return frontendRedirect, err
	}

	// Persist only user tokens — never write admin client_secret onto the DS row.
	if err := m.dsSvc.MergeDataSourceCredentials(ctx, ds.ID, canvasCfg.TokenCredentialsMap()); err != nil {
		return frontendRedirect, err
	}
	return frontendRedirect, nil
}

// IsAuthorized reports whether the data source has an access_token.
func (m *Manager) IsAuthorized(ctx context.Context, ds *types.DataSource) (bool, error) {
	if ds == nil {
		return false, nil
	}
	cfg, err := ds.ParseConfig()
	if err != nil || cfg == nil {
		return false, err
	}
	token := strings.TrimSpace(stringCred(cfg.Credentials, "access_token"))
	return token != "", nil
}

// Revoke clears access/refresh tokens while keeping any leftover fields.
func (m *Manager) Revoke(ctx context.Context, ds *types.DataSource) error {
	if ds == nil {
		return fmt.Errorf("data source is nil")
	}
	patch := map[string]interface{}{
		"access_token":  "",
		"refresh_token": "",
		"expires_at":    "",
	}
	return m.dsSvc.MergeDataSourceCredentials(ctx, ds.ID, patch)
}

func stringCred(creds map[string]interface{}, key string) string {
	if creds == nil {
		return ""
	}
	v, ok := creds[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSpace(s)
}
