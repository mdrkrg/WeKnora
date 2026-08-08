package canvas

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	resourcePrefixCourse = "course:"
	resourcePrefixFolder = "folder:"
	resourcePrefixFile   = "file:"

	resourceTypeCourse = "course"
	resourceTypeFolder = "folder"
	resourceTypeFile   = "file"
)

// Config holds Canvas LMS connector runtime credentials.
// App fields (BaseURL/ClientID/ClientSecret) normally come from workspace admin
// settings; AccessToken/RefreshToken/ExpiresAt are per data-source user tokens.
type Config struct {
	BaseURL      string `json:"base_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"` // RFC3339
}

// GetBaseURL returns the trimmed Canvas instance base URL without a trailing slash.
func (c *Config) GetBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
}

// TokenExpiry parses ExpiresAt; zero time means unknown/expired.
func (c *Config) TokenExpiry() time.Time {
	if c.ExpiresAt == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ApplyAppConfig overlays deployment-level Canvas OAuth app credentials.
func (c *Config) ApplyAppConfig(app *types.CanvasOAuthCredentials) {
	if app == nil {
		return
	}
	if c.BaseURL == "" {
		c.BaseURL = app.BaseURL
	}
	if c.ClientID == "" {
		c.ClientID = app.ClientID
	}
	if c.ClientSecret == "" {
		c.ClientSecret = app.ClientSecret
	}
}

// ParseConfigForOAuth extracts Canvas credentials for the OAuth manager / connector.
// App fields may be empty until ApplyTenantApp is called.
func ParseConfigForOAuth(config *types.DataSourceConfig) (*Config, error) {
	return parseCanvasConfig(config, false)
}

func parseCanvasConfig(config *types.DataSourceConfig, requireApp bool) (*Config, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	creds := config.Credentials
	if creds == nil {
		creds = map[string]interface{}{}
	}

	cfg := &Config{
		BaseURL:      stringCred(creds, "base_url"),
		ClientID:     stringCred(creds, "client_id"),
		ClientSecret: stringCred(creds, "client_secret"),
		AccessToken:  stringCred(creds, "access_token"),
		RefreshToken: stringCred(creds, "refresh_token"),
		ExpiresAt:    stringCred(creds, "expires_at"),
	}
	if requireApp {
		if cfg.GetBaseURL() == "" {
			return nil, fmt.Errorf("canvas base_url is required (configure in workspace settings)")
		}
		if cfg.ClientID == "" || cfg.ClientSecret == "" {
			return nil, fmt.Errorf("canvas client_id/client_secret are required (configure in workspace settings)")
		}
		if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
			return nil, err
		}
	} else if cfg.GetBaseURL() != "" {
		if err := datasource.ValidateConnectorBaseURL(cfg.GetBaseURL()); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func stringCred(creds map[string]interface{}, key string) string {
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

// CredentialsMap returns the full credential map for persistence.
func (c *Config) CredentialsMap() map[string]interface{} {
	return map[string]interface{}{
		"base_url":      c.GetBaseURL(),
		"client_id":     c.ClientID,
		"client_secret": c.ClientSecret,
		"access_token":  c.AccessToken,
		"refresh_token": c.RefreshToken,
		"expires_at":    c.ExpiresAt,
	}
}

// TokenCredentialsMap returns only per-user OAuth tokens (no admin app secrets).
func (c *Config) TokenCredentialsMap() map[string]interface{} {
	return map[string]interface{}{
		"access_token":  c.AccessToken,
		"refresh_token": c.RefreshToken,
		"expires_at":    c.ExpiresAt,
	}
}

func (c *Config) toCredentialsMap() map[string]interface{} {
	return c.CredentialsMap()
}

func encodeCourseID(id int64) string {
	return fmt.Sprintf("%s%d", resourcePrefixCourse, id)
}

func encodeFolderID(id int64) string {
	return fmt.Sprintf("%s%d", resourcePrefixFolder, id)
}

func encodeFileID(id int64) string {
	return fmt.Sprintf("%s%d", resourcePrefixFile, id)
}

func parseResourceID(externalID string) (kind string, id int64, err error) {
	switch {
	case strings.HasPrefix(externalID, resourcePrefixCourse):
		kind = resourceTypeCourse
		_, err = fmt.Sscanf(externalID, resourcePrefixCourse+"%d", &id)
	case strings.HasPrefix(externalID, resourcePrefixFolder):
		kind = resourceTypeFolder
		_, err = fmt.Sscanf(externalID, resourcePrefixFolder+"%d", &id)
	case strings.HasPrefix(externalID, resourcePrefixFile):
		kind = resourceTypeFile
		_, err = fmt.Sscanf(externalID, resourcePrefixFile+"%d", &id)
	default:
		err = fmt.Errorf("unknown canvas resource id %q", externalID)
	}
	return
}
