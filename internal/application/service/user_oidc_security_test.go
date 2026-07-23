package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func withOIDCSSRFWhitelist(t *testing.T, raw string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", raw)
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestOIDCDiscoveryRejectsInternalDiscoveredEndpoint(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(oidcDiscoveryDocument{
			AuthorizationEndpoint: serverURL(r, "/authorize"),
			TokenEndpoint:         "http://169.254.169.254/latest/meta-data/",
		})
	}))
	defer server.Close()

	svc := &userService{}
	cfg := &config.OIDCAuthConfig{DiscoveryURL: server.URL}
	err := svc.populateOIDCEndpoints(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "OIDC token endpoint failed SSRF validation") {
		t.Fatalf("populateOIDCEndpoints error = %v, want token SSRF validation failure", err)
	}
}

func TestOIDCTokenExchangeBlocksRedirectToInternalURL(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	svc := &userService{}
	cfg := &config.OIDCAuthConfig{TokenEndpoint: server.URL, ClientID: "client-id", ClientSecret: "client-secret"}
	_, err := svc.exchangeOIDCCode(context.Background(), cfg, "code", "https://app.example/callback")
	if err == nil || !strings.Contains(err.Error(), secutils.ErrSSRFRedirectBlocked.Error()) {
		t.Fatalf("exchangeOIDCCode error = %v, want redirect SSRF block", err)
	}
}

func TestOIDCErrorsDoNotEchoSecrets(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("client-secret bearer-token"))
	}))
	defer server.Close()

	svc := &userService{}
	cfg := &config.OIDCAuthConfig{TokenEndpoint: server.URL, ClientID: "client-id", ClientSecret: "client-secret"}
	_, err := svc.exchangeOIDCCode(context.Background(), cfg, "code", "https://app.example/callback")
	if err == nil {
		t.Fatalf("exchangeOIDCCode returned nil error")
	}
	if strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("exchangeOIDCCode error leaked secret: %v", err)
	}

	_, err = svc.fetchOIDCUserInfo(context.Background(), server.URL, "bearer-token")
	if err == nil {
		t.Fatalf("fetchOIDCUserInfo returned nil error")
	}
	if strings.Contains(err.Error(), "bearer-token") {
		t.Fatalf("fetchOIDCUserInfo error leaked bearer token: %v", err)
	}
}

func serverURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func buildFakeIDToken(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	header := map[string]interface{}{"alg": "HS256", "typ": "JWT"}
	headerB, _ := json.Marshal(header)
	payloadB, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(headerB) + "." +
		base64.RawURLEncoding.EncodeToString(payloadB) + ".sig"
}

// TestResolveOIDCUserInfoSynthesizesEmailFromSubject verifies that when
// the OIDC provider does not return an email claim and EmailFallbackDomain
// is configured, resolveOIDCUserInfo synthesizes "<sub>@<domain>" from
// the id_token sub claim. The name claim is present, so Username keeps
// its raw value; non-ASCII cleanup and fallback to the email local-part
// happen later in generateOIDCUsername.
func TestResolveOIDCUserInfoSynthesizesEmailFromSubject(t *testing.T) {
	svc := &userService{}
	cfg := &config.OIDCAuthConfig{
		EmailFallbackDomain: "example.org",
		UserInfoMapping:     &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	tokenResp := &oidcTokenResponse{
		IDToken: buildFakeIDToken(t, map[string]interface{}{
			"sub":  "test-user",
			"name": "张三",
		}),
	}

	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, tokenResp)
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo error = %v", err)
	}
	if info.Email != "test-user@example.org" {
		t.Fatalf("expected synthesized email test-user@example.org, got %q", info.Email)
	}
	// name claim present -> Username keeps the raw value; non-ASCII cleanup
	// and fallback to the email local-part happen later in generateOIDCUsername.
	if info.Username != "张三" {
		t.Fatalf("expected raw name claim, got %q", info.Username)
	}
}

// TestResolveOIDCUserInfoSynthesizesEmailAndUsernameWhenNameAbsent
// verifies that when both email and name claims are missing, the email
// is synthesized from sub as above, and Username also falls back to sub
// (since preferred_username and name are both absent).
func TestResolveOIDCUserInfoSynthesizesEmailAndUsernameWhenNameAbsent(t *testing.T) {
	svc := &userService{}
	cfg := &config.OIDCAuthConfig{
		EmailFallbackDomain: "example.org",
		UserInfoMapping:     &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	tokenResp := &oidcTokenResponse{
		IDToken: buildFakeIDToken(t, map[string]interface{}{
			"sub": "test-user",
		}),
	}

	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, tokenResp)
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo error = %v", err)
	}
	if info.Email != "test-user@example.org" {
		t.Fatalf("expected synthesized email, got %q", info.Email)
	}
	if info.Username != "test-user" {
		t.Fatalf("expected username fallback to subject, got %q", info.Username)
	}
}

// TestResolveOIDCUserInfoPreservesEmailWhenPresent verifies that a real
// email claim from the provider is never overwritten by the fallback
// synthesis, even when EmailFallbackDomain is configured.
func TestResolveOIDCUserInfoPreservesEmailWhenPresent(t *testing.T) {
	svc := &userService{}
	cfg := &config.OIDCAuthConfig{
		EmailFallbackDomain: "example.org",
		UserInfoMapping:     &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	tokenResp := &oidcTokenResponse{
		IDToken: buildFakeIDToken(t, map[string]interface{}{
			"sub":   "test-user",
			"name":  "张三",
			"email": "real@example.com",
		}),
	}

	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, tokenResp)
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo error = %v", err)
	}
	if info.Email != "real@example.com" {
		t.Fatalf("expected real email, got %q", info.Email)
	}
}

// TestResolveOIDCUserInfoNoFallbackKeepsEmailEmpty verifies that when
// EmailFallbackDomain is NOT configured, the historical behaviour is
// preserved: a missing email claim stays empty, and LoginWithOIDC will
// later return "OIDC provider did not return email".
func TestResolveOIDCUserInfoNoFallbackKeepsEmailEmpty(t *testing.T) {
	svc := &userService{}
	cfg := &config.OIDCAuthConfig{
		UserInfoMapping: &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	tokenResp := &oidcTokenResponse{
		IDToken: buildFakeIDToken(t, map[string]interface{}{
			"sub":  "test-user",
			"name": "张三",
		}),
	}

	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, tokenResp)
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo error = %v", err)
	}
	if info.Email != "" {
		t.Fatalf("expected empty email when no fallback configured, got %q", info.Email)
	}
}

// TestResolveOIDCUserInfoFallbackSkippedWhenSubjectAbsent verifies that
// synthesis is guarded on a non-empty sub claim: if sub is missing too,
// the email stays empty rather than producing a malformed "@domain".
func TestResolveOIDCUserInfoFallbackSkippedWhenSubjectAbsent(t *testing.T) {
	svc := &userService{}
	cfg := &config.OIDCAuthConfig{
		EmailFallbackDomain: "example.org",
		UserInfoMapping:     &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	tokenResp := &oidcTokenResponse{
		IDToken: buildFakeIDToken(t, map[string]interface{}{
			"name": "张三",
		}),
	}

	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, tokenResp)
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo error = %v", err)
	}
	if info.Email != "" {
		t.Fatalf("expected empty email when subject is absent even with fallback, got %q", info.Email)
	}
}

// TestResolveOIDCUserInfoFallbackSkippedWhenSubjectIsWhitespaceOnly
// verifies that a whitespace-only sub claim is treated as empty (trimmed
// before the guard), so synthesis does not produce a malformed "@domain".
func TestResolveOIDCUserInfoFallbackSkippedWhenSubjectIsWhitespaceOnly(t *testing.T) {
	svc := &userService{}
	cfg := &config.OIDCAuthConfig{
		EmailFallbackDomain: "example.org",
		UserInfoMapping:     &config.OIDCUserInfoMapping{Username: "name", Email: "email"},
	}
	tokenResp := &oidcTokenResponse{
		IDToken: buildFakeIDToken(t, map[string]interface{}{
			"sub":  "  ",
			"name": "张三",
		}),
	}

	info, err := svc.resolveOIDCUserInfo(context.Background(), cfg, tokenResp)
	if err != nil {
		t.Fatalf("resolveOIDCUserInfo error = %v", err)
	}
	if info.Email != "" {
		t.Fatalf("expected empty email when subject is whitespace-only, got %q", info.Email)
	}
}
