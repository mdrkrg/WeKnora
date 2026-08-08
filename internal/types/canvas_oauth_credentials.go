package types

// CanvasOAuthCredentials represents the Canvas OAuth app credentials
// needed to perform OAuth2 authorization-code exchange and token refresh.
//
// These are deployment-level credentials (site-global). They are never
// persisted into tenant-scoped DB records.
type CanvasOAuthCredentials struct {
	BaseURL      string `json:"base_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}
