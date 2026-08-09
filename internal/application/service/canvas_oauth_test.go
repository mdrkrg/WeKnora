package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

func TestCanvasOAuthStatusConfiguredWhenAllEnvSet(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "canvas.example.com")
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.example.com/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "secret")

	svc := NewCanvasOAuthService()
	status, err := svc.CheckStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Configured {
		t.Fatalf("expected configured=true, got %#v", status)
	}
	if status.BaseURL != "https://canvas.example.com" {
		t.Fatalf("expected trimmed base_url, got %q", status.BaseURL)
	}
	if status.ClientID != "client-id" {
		t.Fatalf("expected client_id, got %q", status.ClientID)
	}
}

func TestCanvasOAuthStatusErrorsWhenIncomplete(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "canvas.example.com")
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("CANVAS_OAUTH_BASE_URL", "https://canvas.example.com/")
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "secret")

	svc := NewCanvasOAuthService()
	_, err := svc.CheckStatus(context.Background())
	if err == nil {
		t.Fatalf("expected error for incomplete app credentials")
	}
}
