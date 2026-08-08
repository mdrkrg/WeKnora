package oauth

import (
	"fmt"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// LoadAppCredentials reads the deployment-level (site-global) Canvas OAuth
// app credentials from the CANVAS_OAUTH_* environment variables.
//
// When all three variables are empty it returns (nil, nil): Canvas OAuth is
// disabled and no error is raised. A partially filled set or an invalid
// base_url yields an error so deployments fail fast at startup and surface
// the misconfiguration to administrators instead of failing later per
// request. The client_secret is never persisted to the database.
func LoadAppCredentials() (*types.CanvasOAuthCredentials, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CANVAS_OAUTH_BASE_URL")), "/")
	clientID := strings.TrimSpace(os.Getenv("CANVAS_OAUTH_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("CANVAS_OAUTH_CLIENT_SECRET"))

	enabled := baseURL != "" || clientID != "" || clientSecret != ""
	if !enabled {
		return nil, nil
	}

	var errs []string
	if baseURL == "" {
		errs = append(errs, "canvas_oauth.base_url is required when canvas_oauth is enabled")
	}
	if clientID == "" {
		errs = append(errs, "canvas_oauth.client_id is required when canvas_oauth is enabled")
	}
	if clientSecret == "" {
		errs = append(errs, "canvas_oauth.client_secret is required when canvas_oauth is enabled")
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	if err := datasource.ValidateConnectorBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("canvas_oauth.base_url is invalid: %w", err)
	}

	return &types.CanvasOAuthCredentials{
		BaseURL:      baseURL,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}, nil
}
