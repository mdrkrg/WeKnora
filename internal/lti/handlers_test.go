package lti

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lti/ltitest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func testLTIHandler(t *testing.T, deps *handlerDeps) *Handler {
	t.Helper()
	if deps == nil {
		deps = &handlerDeps{}
	}
	cfg := &config.LTIConfig{
		Enable:              true,
		HandoffURL:          "https://app.example.com/api/auth/lti/handoff",
		HandoffSharedSecret: "redeem-secret",
		LaunchURL:           "https://tool.example.com/lti/launch",
		FrameAncestors:      "'self'",
		NonceMaxAge:         10 * time.Minute,
		TicketTTL:           120 * time.Second,
	}
	if deps.cfg != nil {
		cfg = deps.cfg
	}
	registrations := deps.registrations
	if registrations == nil {
		registrations = &fakeRegistrationStore{}
	}
	tickets := deps.tickets
	if tickets == nil {
		tickets = &fakeTicketService{}
	}
	keys := deps.keys
	if keys == nil {
		keys = &fakeToolKeyStore{}
	}
	verifier := deps.verifier
	if verifier == nil {
		verifier = NewVerifier(&fakeKeysets{})
	}
	resolver := deps.resolver
	if resolver == nil {
		resolver = &fakeResolver{}
	}
	minter := deps.minter
	if minter == nil {
		minter = &fakeMinter{}
	}
	return NewHandler(cfg, registrations, tickets, keys, verifier, resolver, minter, deps.audit)
}

type handlerDeps struct {
	cfg           *config.LTIConfig
	registrations RegistrationStore
	tickets       TicketService
	keys          ToolKeyStore
	verifier      *Verifier
	resolver      IdentityResolver
	minter        TokenMinter
	audit         AuditSink
}

func postForm(t *testing.T, h *Handler, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	newTestEngine(h).ServeHTTP(w, req)
	return w
}

func postLaunch(t *testing.T, h *Handler, idToken, state string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/lti/launch", strings.NewReader(url.Values{
		"id_token": {idToken},
		"state":    {state},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	newTestEngine(h).ServeHTTP(w, req)
	return w
}

func redeemPost(t *testing.T, h *Handler, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/lti/tickets/redeem", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	newTestEngine(h).ServeHTTP(w, req)
	return w
}

func newTestEngine(h *Handler) *gin.Engine {
	r := gin.New()
	r.POST("/lti/login_initiations", h.LoginInitiation)
	r.POST("/lti/launch", h.Launch)
	r.GET("/.well-known/jwks.json", h.JWKS)
	r.POST("/lti/tickets/redeem", h.Redeem)
	r.GET("/lti/handoff", h.Handoff)
	return r
}

func getHandoff(t *testing.T, h *Handler, ticket string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/lti/handoff?ticket="+url.QueryEscape(ticket), nil)
	newTestEngine(h).ServeHTTP(w, req)
	return w
}

func TestLoginInitiationUnknownRegistration(t *testing.T) {
	h := testLTIHandler(t, nil)
	w := postForm(t, h, "/lti/login_initiations", url.Values{
		"iss":       {"https://unknown.example.com"},
		"client_id": {"client-x"},
	})
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "LTI")
}

func TestLoginInitiationDisabledRegistration(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{
		{ID: 1, Issuer: "https://platform.example.com", ClientID: "client-1", Enabled: false},
	}}
	h := testLTIHandler(t, &handlerDeps{registrations: regs})
	w := postForm(t, h, "/lti/login_initiations", url.Values{
		"iss":       {"https://platform.example.com"},
		"client_id": {"client-1"},
	})
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestLoginInitiationRedirectsToAuthEndpoint(t *testing.T) {
	regs := &fakeRegistrationStore{regs: []*Registration{
		{
			ID:           1,
			Issuer:       "https://platform.example.com",
			ClientID:     "client-1",
			AuthEndpoint: "https://platform.example.com/authorize",
			Enabled:      true,
		},
	}}
	tickets := &fakeTicketService{}
	h := testLTIHandler(t, &handlerDeps{registrations: regs, tickets: tickets})
	w := postForm(t, h, "/lti/login_initiations", url.Values{
		"iss":              {"https://platform.example.com"},
		"client_id":        {"client-1"},
		"login_hint":       {"platform-sub-uuid"},
		"target_link_uri":  {"https://tool.example.com/lti/launch"},
		"lti_message_hint": {"opaque"},
	})
	require.Equal(t, http.StatusFound, w.Code)

	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "https://platform.example.com", loc.Scheme+"://"+loc.Host)
	require.Equal(t, "/authorize", loc.Path)

	q := loc.Query()
	require.Equal(t, "id_token", q.Get("response_type"))
	require.Equal(t, "openid", q.Get("scope"))
	require.Equal(t, "form_post", q.Get("response_mode"))
	require.Equal(t, "client-1", q.Get("client_id"))
	require.Equal(t, "https://tool.example.com/lti/launch", q.Get("redirect_uri"))
	require.Equal(t, "platform-sub-uuid", q.Get("login_hint"))
	require.Equal(t, "https://tool.example.com/lti/launch", q.Get("target_link_uri"))
	require.Equal(t, "opaque", q.Get("lti_message_hint"))
	require.NotEmpty(t, q.Get("nonce"))
	require.NotEmpty(t, q.Get("state"))

	st, err := VerifyNonceState(q.Get("state"), 10*time.Minute)
	require.NoError(t, err)
	require.Equal(t, q.Get("nonce"), st.Nonce)
}

func TestLoginInitiationMissingIss(t *testing.T) {
	h := testLTIHandler(t, nil)
	w := postForm(t, h, "/lti/login_initiations", url.Values{
		"client_id": {"client-1"},
	})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLaunchValidFlow(t *testing.T) {
	p := ltitest.NewPlatform(t)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-abc")
	require.NoError(t, err)

	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	keys, err := p.Keyfunc()
	require.NoError(t, err)
	resolver := &fakeResolver{res: &IdentityResolution{UserID: "weknora-user-1"}}
	tickets := &fakeTicketService{raw: "ticket-raw-1"}
	h := testLTIHandler(t, &handlerDeps{
		registrations: regs,
		verifier:      NewVerifier(&fakeKeysets{kf: keys}),
		resolver:      resolver,
		tickets:       tickets,
	})

	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	w := postLaunch(t, h, tok, state)

	require.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	require.Equal(t, "https://app.example.com/api/auth/lti/handoff?ticket=ticket-raw-1", loc)
	require.Equal(t, []string{"weknora-user-1", "course-42"}, tickets.issueArgs)
	require.Equal(t, "ticket-raw-1", tickets.raw)
}

func TestLaunchBadState(t *testing.T) {
	p := ltitest.NewPlatform(t)
	h := testLTIHandler(t, &handlerDeps{
		registrations: &fakeRegistrationStore{
			regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")},
		},
		verifier: NewVerifier(&fakeKeysets{}),
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/lti/launch", strings.NewReader(url.Values{
		"id_token": {ltiClaims(p, nil)},
		"state":    {"garbage"},
	}.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.Launch(c)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLaunchRegistrationNotFound(t *testing.T) {
	p := ltitest.NewPlatform(t)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-abc")
	require.NoError(t, err)
	tok := ltiClaims(p, func(m jwt.MapClaims) {
		m["nonce"] = "nonce-abc"
		m["iss"] = "https://not-registered.example.com"
	})
	h := testLTIHandler(t, &handlerDeps{
		registrations: &fakeRegistrationStore{},
		verifier:      NewVerifier(&fakeKeysets{}),
	})
	w := postLaunch(t, h, tok, state)
	// The verifier alone cannot discover the registration; the handler must
	// fail before claiming the token is valid: 401 means "untrusted".
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLaunchIdentityResolutionDisabled(t *testing.T) {
	p := ltitest.NewPlatform(t)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-abc")
	require.NoError(t, err)
	keys, err := p.Keyfunc()
	require.NoError(t, err)
	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	h := testLTIHandler(t, &handlerDeps{
		registrations: &fakeRegistrationStore{
			regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")},
		},
		verifier: NewVerifier(&fakeKeysets{kf: keys}),
		resolver: &fakeResolver{err: ErrIdentityDisabled},
	})
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func setupNonceEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-lti-handler-secret")
	resetNonceSecret()
	t.Cleanup(resetNonceSecret)
}

func TestJWKSPublishesToolKeys(t *testing.T) {
	keys := &fakeToolKeyStore{key: &ToolKey{
		KID:       "tool-kid-1",
		PublicJWK: `{"kty":"RSA","kid":"tool-kid-1","n":"AAAA","e":"AQAB"}`,
	}}
	h := testLTIHandler(t, &handlerDeps{keys: keys})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	newTestEngine(h).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Keys, 1)
	require.Equal(t, "tool-kid-1", body.Keys[0]["kid"])
	require.Equal(t, "RSA", body.Keys[0]["kty"])
}

func TestRedeemRequiresSharedSecret(t *testing.T) {
	h := testLTIHandler(t, nil)
	w := redeemPost(t, h, "", `{"ticket":"x"}`)
	require.Equal(t, http.StatusUnauthorized, w.Code)

	w = redeemPost(t, h, "wrong-secret", `{"ticket":"x"}`)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRedeemMintsDefaultTenant(t *testing.T) {
	minter := &fakeMinter{defaultResult: &TokenResult{AccessToken: "at", RefreshToken: "rt"}}
	tickets := &fakeTicketService{consumeRes: &Ticket{
		UserID:    "weknora-user-1",
		ContextID: "course-42",
		Roles:     `["role-a"]`,
	}}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets, minter: minter})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "at", body["access_token"])
	require.Equal(t, "rt", body["refresh_token"])
	require.Equal(t, "weknora-user-1", body["user_id"])
	require.Equal(t, "course-42", body["context_id"])
	require.Equal(t, "weknora-user-1", minter.lastDefaultUID)
}

func TestRedeemMintsForTargetTenant(t *testing.T) {
	minter := &fakeMinter{forTenantRes: &TokenResult{AccessToken: "at2", RefreshToken: "rt2"}}
	tickets := &fakeTicketService{consumeRes: &Ticket{UserID: "weknora-user-1"}}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets, minter: minter})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1","tenant_id":7}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, uint64(7), minter.lastTenantID)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "at2", body["access_token"])
}

func TestRedeemRejectsNonMember(t *testing.T) {
	minter := &fakeMinter{forTenantErr: ErrNotTenantMember}
	tickets := &fakeTicketService{consumeRes: &Ticket{UserID: "weknora-user-1"}}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets, minter: minter})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1","tenant_id":7}`)
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestRedeemRejectsConsumedTicket(t *testing.T) {
	tickets := &fakeTicketService{consumeErr: ErrTicketConsumed}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1"}`)
	require.Equal(t, http.StatusConflict, w.Code)
}

func TestRedeemRejectsExpiredTicket(t *testing.T) {
	tickets := &fakeTicketService{consumeErr: ErrTicketExpired}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1"}`)
	require.Equal(t, http.StatusGone, w.Code)

	tickets2 := &fakeTicketService{consumeErr: ErrTicketNotFound}
	h2 := testLTIHandler(t, &handlerDeps{tickets: tickets2})
	w2 := redeemPost(t, h2, "redeem-secret", `{"ticket":"raw-1"}`)
	require.Equal(t, http.StatusGone, w2.Code)
}

func TestLaunchAuditsTicketIssued(t *testing.T) {
	p := ltitest.NewPlatform(t)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-abc")
	require.NoError(t, err)

	regs := &fakeRegistrationStore{regs: []*Registration{baseRegistration("https://platform.example.com", "client-1")}}
	keys, err := p.Keyfunc()
	require.NoError(t, err)
	audit := &fakeAuditSink{}
	h := testLTIHandler(t, &handlerDeps{
		registrations: regs,
		verifier:      NewVerifier(&fakeKeysets{kf: keys}),
		resolver:      &fakeResolver{res: &IdentityResolution{UserID: "weknora-user-1"}},
		tickets:       &fakeTicketService{raw: "ticket-raw-1"},
		audit:         audit,
	})

	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusFound, w.Code)

	require.Len(t, audit.entries, 1)
	entry := audit.entries[0]
	require.Equal(t, AuditActionLTITicketIssued, entry.Action)
	require.Equal(t, "weknora-user-1", entry.ActorUserID)
	require.Equal(t, types.AuditOutcomeSuccess, entry.Outcome)
	details := string(entry.Details)
	require.Contains(t, details, `"iss":"https://platform.example.com"`)
	require.Contains(t, details, `"context_id":"course-42"`)
}

func TestRedeemAuditsSuccess(t *testing.T) {
	audit := &fakeAuditSink{}
	minter := &fakeMinter{defaultResult: &TokenResult{AccessToken: "at", RefreshToken: "rt"}}
	tickets := &fakeTicketService{consumeRes: &Ticket{
		UserID:    "weknora-user-1",
		ContextID: "course-42",
	}}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets, minter: minter, audit: audit})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1"}`)
	require.Equal(t, http.StatusOK, w.Code)

	require.Len(t, audit.entries, 1)
	entry := audit.entries[0]
	require.Equal(t, AuditActionLTITicketRedeemed, entry.Action)
	require.Equal(t, "weknora-user-1", entry.ActorUserID)
	require.Equal(t, types.AuditOutcomeSuccess, entry.Outcome)
}

func TestRedeemAuditsReplayDenied(t *testing.T) {
	audit := &fakeAuditSink{}
	tickets := &fakeTicketService{consumeErr: ErrTicketConsumed}
	h := testLTIHandler(t, &handlerDeps{tickets: tickets, audit: audit})

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"raw-1"}`)
	require.Equal(t, http.StatusConflict, w.Code)

	require.Len(t, audit.entries, 1)
	entry := audit.entries[0]
	require.Equal(t, AuditActionLTITicketRedeemDenied, entry.Action)
	require.Equal(t, types.AuditOutcomeDenied, entry.Outcome)
	require.Contains(t, string(entry.Details), `"reason":"consumed"`)
}

func TestRedeemDoesNotAuditBadSecret(t *testing.T) {
	audit := &fakeAuditSink{}
	h := testLTIHandler(t, &handlerDeps{audit: audit})

	w := redeemPost(t, h, "wrong-secret", `{"ticket":"x"}`)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Empty(t, audit.entries)
}

func TestHandoffInertWhenDisabled(t *testing.T) {
	h := testLTIHandler(t, nil)
	w := getHandoff(t, h, "whatever")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandoffDeliversSessionViaHash(t *testing.T) {
	audit := &fakeAuditSink{}
	minter := &fakeMinter{defaultResult: &TokenResult{AccessToken: "at", RefreshToken: "rt"}}
	tickets := &fakeTicketService{consumeRes: &Ticket{
		UserID:    "weknora-user-1",
		ContextID: "course-42",
	}}
	h := testLTIHandler(t, &handlerDeps{
		cfg: &config.LTIConfig{
			Enable:              true,
			HandoffURL:          "https://app.example.com/api/auth/lti/handoff",
			HandoffSharedSecret: "redeem-secret",
			LaunchURL:           "https://tool.example.com/lti/launch",
			FrameAncestors:      "'self'",
			NonceMaxAge:         10 * time.Minute,
			TicketTTL:           120 * time.Second,
			SelfHandoffEnable:   true,
		},
		tickets: tickets,
		minter:  minter,
		audit:   audit,
	})

	w := getHandoff(t, h, "raw-1")
	require.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	require.Contains(t, loc, "/#lti_result=")

	// Same decoding the SPA performs.
	raw := strings.TrimPrefix(loc, "/#lti_result=")
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(decoded, &payload))
	require.Equal(t, true, payload["success"])
	require.Equal(t, "at", payload["token"])
	require.Equal(t, "rt", payload["refresh_token"])
	require.Equal(t, "weknora-user-1", payload["user_id"])
	require.Equal(t, "course-42", payload["context_id"])

	// Parity with the S2S redeem audit.
	require.Len(t, audit.entries, 1)
	require.Equal(t, AuditActionLTITicketRedeemed, audit.entries[0].Action)
}

func TestHandoffRedirectsErrorOnConsumedTicket(t *testing.T) {
	audit := &fakeAuditSink{}
	tickets := &fakeTicketService{consumeErr: ErrTicketConsumed}
	h := testLTIHandler(t, &handlerDeps{
		cfg: &config.LTIConfig{
			Enable:              true,
			HandoffURL:          "https://app.example.com/api/auth/lti/handoff",
			HandoffSharedSecret: "redeem-secret",
			LaunchURL:           "https://tool.example.com/lti/launch",
			FrameAncestors:      "'self'",
			NonceMaxAge:         10 * time.Minute,
			TicketTTL:           120 * time.Second,
			SelfHandoffEnable:   true,
		},
		tickets: tickets,
		audit:   audit,
	})

	w := getHandoff(t, h, "raw-1")
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/#lti_error=invalid_ticket")

	require.Len(t, audit.entries, 1)
	require.Equal(t, AuditActionLTITicketRedeemDenied, audit.entries[0].Action)
	require.Contains(t, string(audit.entries[0].Details), `"reason":"consumed"`)
}

func TestHandoffMissingTicketRedirectsError(t *testing.T) {
	h := testLTIHandler(t, &handlerDeps{
		cfg: &config.LTIConfig{Enable: true, SelfHandoffEnable: true},
	})
	w := getHandoff(t, h, "")
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "/#lti_error=missing_ticket")
}
