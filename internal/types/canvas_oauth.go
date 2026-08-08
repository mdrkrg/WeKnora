package types

// CanvasOAuthStatusResult reports whether the workspace Canvas OAuth app is configured.
type CanvasOAuthStatusResult struct {
	Configured bool   `json:"configured"`
	BaseURL    string `json:"base_url,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
}
