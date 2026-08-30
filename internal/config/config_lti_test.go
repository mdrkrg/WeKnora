package config

import (
	"testing"
	"time"
)

// TestApplyLTIEnvOverrides_SecureDefaults pins the LTI_* defaults: without
// them an unset group would yield zero values (no CSP, nonce/ticket never
// expire).
func TestApplyLTIEnvOverrides_SecureDefaults(t *testing.T) {
	t.Setenv("LTI_ENABLE", "")
	t.Setenv("LTI_NONCE_MAX_AGE", "")
	t.Setenv("LTI_TICKET_TTL", "")
	t.Setenv("LTI_FRAME_ANCESTORS", "")

	cfg := &Config{LTI: &LTIConfig{}}
	applyLTIEnvOverrides(cfg)

	if cfg.LTI.Enable {
		t.Fatal("LTI should default to disabled")
	}
	if cfg.LTI.NonceMaxAge != 10*time.Minute {
		t.Fatalf("NonceMaxAge = %v, want 10m", cfg.LTI.NonceMaxAge)
	}
	if cfg.LTI.TicketTTL != 120*time.Second {
		t.Fatalf("TicketTTL = %v, want 120s", cfg.LTI.TicketTTL)
	}
	if cfg.LTI.FrameAncestors != "'self'" {
		t.Fatalf("FrameAncestors = %q, want 'self'", cfg.LTI.FrameAncestors)
	}
}

// TestApplyLTIEnvOverrides_DurationGuard pins the guarded duration parse:
// valid values are honored, malformed or non-positive ones keep the default.
func TestApplyLTIEnvOverrides_DurationGuard(t *testing.T) {
	cases := []struct {
		name      string
		nonceAge  string
		ticketTTL string
		wantNonce time.Duration
		wantTTL   time.Duration
	}{
		{"valid values are honored", "30s", "60s", 30 * time.Second, 60 * time.Second},
		{"malformed duration keeps default", "not-a-duration", "oops", 10 * time.Minute, 120 * time.Second},
		{"non-positive duration keeps default", "-5s", "-5s", 10 * time.Minute, 120 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LTI_NONCE_MAX_AGE", tc.nonceAge)
			t.Setenv("LTI_TICKET_TTL", tc.ticketTTL)

			cfg := &Config{LTI: &LTIConfig{}}
			applyLTIEnvOverrides(cfg)

			if cfg.LTI.NonceMaxAge != tc.wantNonce {
				t.Fatalf("NonceMaxAge = %v, want %v", cfg.LTI.NonceMaxAge, tc.wantNonce)
			}
			if cfg.LTI.TicketTTL != tc.wantTTL {
				t.Fatalf("TicketTTL = %v, want %v", cfg.LTI.TicketTTL, tc.wantTTL)
			}
		})
	}
}

// TestApplyLTIEnvOverrides_EnableCaseInsensitive pins that LTI_ENABLE is a
// case-insensitive boolean and honors an explicit false.
func TestApplyLTIEnvOverrides_EnableCaseInsensitive(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "True"} {
		t.Setenv("LTI_ENABLE", v)

		cfg := &Config{LTI: &LTIConfig{}}
		applyLTIEnvOverrides(cfg)

		if !cfg.LTI.Enable {
			t.Fatalf("LTI_ENABLE=%s should enable LTI", v)
		}
	}

	t.Setenv("LTI_ENABLE", "false")
	cfg := &Config{LTI: &LTIConfig{}}
	applyLTIEnvOverrides(cfg)
	if cfg.LTI.Enable {
		t.Fatal("LTI_ENABLE=false should keep LTI disabled")
	}
}

func TestApplyLTIEnvOverrides_SelfHandoff(t *testing.T) {
	t.Setenv("LTI_SELF_HANDOFF_ENABLE", "")
	cfg := &Config{LTI: &LTIConfig{}}
	applyLTIEnvOverrides(cfg)
	if cfg.LTI.SelfHandoffEnable {
		t.Fatal("self-handoff should default to disabled")
	}

	t.Setenv("LTI_SELF_HANDOFF_ENABLE", "true")
	cfg = &Config{LTI: &LTIConfig{}}
	applyLTIEnvOverrides(cfg)
	if !cfg.LTI.SelfHandoffEnable {
		t.Fatal("LTI_SELF_HANDOFF_ENABLE=true should enable self-handoff")
	}
}
