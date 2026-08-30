package lti

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lti/ltitest"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestLaunchURLUsesConfiguredValue(t *testing.T) {
	h := testLTIHandler(t, &handlerDeps{cfg: &config.LTIConfig{LaunchURL: "https://tool.example.com/lti/launch/"}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/lti/launch", nil)
	require.Equal(t, "https://tool.example.com/lti/launch", h.launchURL(c))
}

func TestLaunchURLDerivedFromRequest(t *testing.T) {
	newCtx := func(proto string, withTLS bool) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodPost, "/lti/launch", nil)
		req.Host = "tool.example.com"
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		if withTLS {
			req.TLS = &tls.ConnectionState{}
		}
		c.Request = req
		return c
	}
	h := testLTIHandler(t, &handlerDeps{cfg: &config.LTIConfig{}})

	require.Equal(t, "https://tool.example.com/lti/launch", h.launchURL(newCtx("https", false)))
	require.Equal(t, "https://tool.example.com/lti/launch", h.launchURL(newCtx("https,http", false)))
	require.Equal(t, "http://tool.example.com/lti/launch", h.launchURL(newCtx("", false)))
	require.Equal(t, "https://tool.example.com/lti/launch", h.launchURL(newCtx("", true)))
}

func TestLaunchMissingArgsRejected(t *testing.T) {
	h := testLTIHandler(t, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/lti/launch", strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	newTestEngine(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLaunchNonceMismatchRejected(t *testing.T) {
	p := ltitest.NewPlatform(t)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-other")
	require.NoError(t, err)
	keys, err := p.Keyfunc()
	require.NoError(t, err)
	h := testLTIHandler(t, &handlerDeps{
		registrations: &fakeRegistrationStore{
			regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")},
		},
		verifier: NewVerifier(&fakeKeysets{kf: keys}),
		resolver: &fakeResolver{res: &IdentityResolution{UserID: "weknora-user-1"}},
	})
	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLaunchTicketIssueErrorRenders500(t *testing.T) {
	p := ltitest.NewPlatform(t)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-abc")
	require.NoError(t, err)
	keys, err := p.Keyfunc()
	require.NoError(t, err)
	h := testLTIHandler(t, &handlerDeps{
		registrations: &fakeRegistrationStore{
			regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")},
		},
		verifier: NewVerifier(&fakeKeysets{kf: keys}),
		resolver: &fakeResolver{res: &IdentityResolution{UserID: "weknora-user-1"}},
		tickets:  &fakeTicketService{issueErr: errors.New("store down")},
	})
	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestLaunchHandoffUnconfiguredRenders500(t *testing.T) {
	p := ltitest.NewPlatform(t)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-abc")
	require.NoError(t, err)
	keys, err := p.Keyfunc()
	require.NoError(t, err)
	cfg := &config.LTIConfig{
		Enable:              true,
		HandoffURL:          "",
		HandoffSharedSecret: "redeem-secret",
		LaunchURL:           "https://tool.example.com/lti/launch",
		FrameAncestors:      "'self'",
		NonceMaxAge:         10 * time.Minute,
		TicketTTL:           120 * time.Second,
	}
	h := testLTIHandler(t, &handlerDeps{
		cfg: cfg,
		registrations: &fakeRegistrationStore{
			regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")},
		},
		verifier: NewVerifier(&fakeKeysets{kf: keys}),
		resolver: &fakeResolver{res: &IdentityResolution{UserID: "weknora-user-1"}},
		tickets:  &fakeTicketService{raw: "ticket-raw-1"},
	})
	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestJWKSKeyEnsureErrorRenders500(t *testing.T) {
	h := testLTIHandler(t, &handlerDeps{keys: &fakeToolKeyStore{err: errors.New("gen failed")}})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	newTestEngine(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestJWKSCorruptPublicJWKRenders500(t *testing.T) {
	h := testLTIHandler(t, &handlerDeps{keys: &fakeToolKeyStore{key: &ToolKey{KID: "k", PublicJWK: "not-json"}}})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	newTestEngine(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRedeemRejectsInvalidJSON(t *testing.T) {
	h := testLTIHandler(t, nil)
	w := redeemPost(t, h, "redeem-secret", "not-json")
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRedeemRejectsMissingTicket(t *testing.T) {
	h := testLTIHandler(t, nil)
	w := redeemPost(t, h, "redeem-secret", `{}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRedeemMinterErrorRenders500(t *testing.T) {
	minter := &fakeMinter{forTenantErr: errors.New("token service down")}
	tickets := &fakeTicketService{consumeRes: &Ticket{UserID: "weknora-user-1"}}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets, minter: minter})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1","tenant_id":7}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// flakyMinter fails the first N mint calls, then succeeds (transient outage).
type flakyMinter struct {
	remainingFailures int
	firstErr          error
	ok                *TokenResult
}

func (m *flakyMinter) issue() (*TokenResult, error) {
	if m.remainingFailures > 0 {
		m.remainingFailures--
		return nil, m.firstErr
	}
	return m.ok, nil
}

func (m *flakyMinter) IssueDefault(context.Context, string) (*TokenResult, error) {
	return m.issue()
}

func (m *flakyMinter) IssueForTenant(context.Context, string, uint64) (*TokenResult, error) {
	return m.issue()
}

// Pins the redeem lifecycle: consume-first wins the single-use race, but a
// mint failure must hand the ticket back so the same ticket redeems again
// instead of a 409 lockout.
func TestRedeemMintFailureDoesNotBurnTicket(t *testing.T) {
	cases := []struct {
		name        string
		firstErr    error
		firstStatus int
	}{
		{"transient minter outage", errors.New("token service down"), http.StatusInternalServerError},
		{"no default workspace denial", ErrNoWorkspace, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			minter := &flakyMinter{
				remainingFailures: 1,
				firstErr:          tc.firstErr,
				ok:                &TokenResult{AccessToken: "at", RefreshToken: "rt"},
			}
			store := &fakeTicketStore{}
			tickets := NewTicketService(store, time.Minute)
			h := testLTIHandler(t, &handlerDeps{tickets: tickets, minter: minter})

			raw := "raw-retry-1"
			require.NoError(t, store.Create(context.Background(), &Ticket{
				TokenHash: hashToken(raw),
				UserID:    "weknora-user-1",
				ExpiresAt: time.Now().Add(time.Minute),
			}))

			// First redemption hits the mint failure.
			w := redeemPost(t, h, "redeem-secret", `{"ticket":"`+raw+`"}`)
			require.Equal(t, tc.firstStatus, w.Code)

			// The same ticket must redeem again: a failed mint is not a
			// successful exchange, so it must not consume the ticket.
			w = redeemPost(t, h, "redeem-secret", `{"ticket":"`+raw+`"}`)
			require.Equal(t, http.StatusOK, w.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, "at", body["access_token"])
		})
	}
}
