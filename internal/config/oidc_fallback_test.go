package config

import (
	"strings"
	"testing"
)

// oidcFallbackConfig returns a valid OIDC-enabled config with the email
// fallback domain set, so ValidateConfig failures below are exclusively
// about the registration-mode constraint.
func oidcFallbackConfig(mode string) *Config {
	cfg := &Config{
		OIDCAuth: &OIDCAuthConfig{
			Enable:             true,
			ClientID:           "client-id",
			ClientSecret:       "client-secret",
			DiscoveryURL:       "https://issuer.example/.well-known/openid-configuration",
			EmailFallbackDomain: "sjtu.edu.cn",
		},
	}
	if mode != "" {
		cfg.Auth = &AuthConfig{RegistrationMode: mode}
	}
	return cfg
}

// TestValidateConfig_EmailFallbackDomainRequiresInviteOnly pins the
// security constraint for OIDC_AUTH_EMAIL_FALLBACK_DOMAIN: synthesized
// emails are not provider-verified, so open registration would let a
// third party pre-register a synthesized address and hijack the OIDC
// login that later links to it. Therefore a config that enables the
// fallback while registration is NOT invite_only must fail validation.
func TestValidateConfig_EmailFallbackDomainRequiresInviteOnly(t *testing.T) {
	cases := []struct {
		name        string
		cfg         *Config
		wantErr     bool
		wantMessage string
	}{
		{
			name:        "fallback + self_serve rejected",
			cfg:         oidcFallbackConfig(AuthRegistrationModeSelfServe),
			wantErr:     true,
			wantMessage: "invite_only",
		},
		{
			name:        "fallback + nil Auth rejected (defaults to self_serve)",
			cfg:         oidcFallbackConfig(""),
			wantErr:     true,
			wantMessage: "invite_only",
		},
		{
			name:    "fallback + invite_only accepted",
			cfg:     oidcFallbackConfig(AuthRegistrationModeInviteOnly),
			wantErr: false,
		},
		{
			name: "oidc disabled + fallback + self_serve accepted",
			cfg: &Config{
				OIDCAuth: &OIDCAuthConfig{Enable: false, EmailFallbackDomain: "sjtu.edu.cn"},
				Auth:     &AuthConfig{RegistrationMode: AuthRegistrationModeSelfServe},
			},
			wantErr: false,
		},
		{
			name: "oidc enabled without fallback + self_serve accepted",
			cfg: &Config{
				OIDCAuth: &OIDCAuthConfig{
					Enable:       true,
					ClientID:     "client-id",
					ClientSecret: "client-secret",
					DiscoveryURL: "https://issuer.example/.well-known/openid-configuration",
				},
				Auth: &AuthConfig{RegistrationMode: AuthRegistrationModeSelfServe},
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("ValidateConfig unexpectedly accepted fallback + open registration")
				}
				if !strings.Contains(err.Error(), tc.wantMessage) {
					t.Fatalf("error = %q, want it to mention %q", err.Error(), tc.wantMessage)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateConfig error = %v, want nil", err)
			}
		})
	}
}
