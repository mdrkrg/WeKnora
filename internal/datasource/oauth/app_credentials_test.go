package oauth

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestLoadAppCredentialsDisabledAllowsEmpty(t *testing.T) {
	t.Setenv("CANVAS_OAUTH_BASE_URL", "")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "")

	creds, err := LoadAppCredentials()
	require.NoError(t, err)
	require.Nil(t, creds)
}

func TestLoadAppCredentialsEnabledRequiresAllFields(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "canvas.test")
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.test/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "secret")

	_, err := LoadAppCredentials()
	require.Error(t, err)
	require.Contains(t, err.Error(), "client_id is required")
}

func TestLoadAppCredentialsEnabledValidatesBaseURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "canvas.test")
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.test/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "secret")

	creds, err := LoadAppCredentials()
	require.NoError(t, err)
	require.NotNil(t, creds)
	require.Equal(t, "https://canvas.test", creds.BaseURL)
	require.Equal(t, "client-id", creds.ClientID)
	require.Equal(t, "secret", creds.ClientSecret)
}

func TestLoadAppCredentialsRejectsInvalidBaseURL(t *testing.T) {
	t.Setenv("CANVAS_OAUTH_BASE_URL", "http://127.0.0.1:3080")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "secret")

	_, err := LoadAppCredentials()
	require.Error(t, err)
	require.Contains(t, err.Error(), "base_url is invalid")
}
