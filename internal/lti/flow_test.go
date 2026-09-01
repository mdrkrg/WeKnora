package lti

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lti/ltitest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// flowUserService stands in for *service.userService: it implements the
// IssueLTITokens slice the real minter adapts, so the full chain exercises
// the real minter mapping (ErrMembershipNotFound -> ErrNotTenantMember).
type flowUserService struct {
	interfaces.UserService
}

func (s *flowUserService) IssueLTITokens(
	_ context.Context, _ string, tenantID uint64, requireMembership bool,
) (string, string, error) {
	if requireMembership && tenantID == 999 {
		return "", "", service.ErrMembershipNotFound
	}
	return "at-flow", "rt-flow", nil
}

// newFlowHandler wires the real protocol stack over real HTTP: the verifier
// pulls the platform JWKS from ltitest's live endpoint, the ticket service is
// the real single-use store, and identity resolution runs the real matcher.
// selfHandoff enables the built-in browser handoff endpoint.
func newFlowHandler(t *testing.T, selfHandoff bool) (*Handler, *ltitest.Platform, *fakeIdentityStore) {
	t.Helper()
	setupSSRFWhitelist(t)
	setupNonceEnv(t)
	p := ltitest.NewPlatform(t)

	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.JWKSURI = p.JWKSURL()
	reg.PublicKeyset = ""
	regs := &fakeRegistrationStore{regs: []*Registration{reg}}

	ids := &fakeIdentityStore{}
	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"student@example.com": {ID: "weknora-user-1"},
	}}

	deps := &handlerDeps{
		registrations: regs,
		tickets:       NewTicketService(&fakeTicketStore{}, 120*time.Second),
		verifier:      NewVerifier(NewKeysetResolver(regs, nil)),
		resolver:      newTestMatcher(users, ids, &fakeAuditSink{}),
		minter:        NewUserTokenMinter(&flowUserService{}),
	}
	if selfHandoff {
		deps.cfg = &config.LTIConfig{
			Enable:              true,
			HandoffURL:          "https://app.example.com/api/auth/lti/handoff",
			HandoffSharedSecret: "redeem-secret",
			LaunchURL:           "https://tool.example.com/lti/launch",
			FrameAncestors:      "'self'",
			NonceMaxAge:         10 * time.Minute,
			TicketTTL:           120 * time.Second,
			SelfHandoffEnable:   true,
		}
	}
	h := testLTIHandler(t, deps)
	return h, p, ids
}

// completeLaunch drives the OIDC third-party initiation and the launch
// POST, returning the single-use ticket issued at the handoff.
func completeLaunch(t *testing.T, h *Handler, p *ltitest.Platform) string {
	t.Helper()

	w := postForm(t, h, "/lti/login_initiations", url.Values{
		"iss":             {"https://platform.example.com"},
		"client_id":       {"client-1"},
		"login_hint":      {"platform-sub-uuid"},
		"target_link_uri": {"https://tool.example.com/lti/launch"},
	})
	require.Equal(t, http.StatusFound, w.Code)
	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "https://tool.example.com/lti/launch", loc.Query().Get("redirect_uri"))
	state := loc.Query().Get("state")
	nonce := loc.Query().Get("nonce")
	require.NotEmpty(t, state)
	require.NotEmpty(t, nonce)

	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = nonce })
	w = postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusFound, w.Code)
	loc, err = url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "https://app.example.com/api/auth/lti/handoff", loc.Scheme+"://"+loc.Host+loc.Path)
	ticket := loc.Query().Get("ticket")
	require.NotEmpty(t, ticket)
	return ticket
}

// TestFullLaunchToRedeemFlow walks the complete LTI 1.3 handshake over real
// HTTP: 3rd-party initiation -> launch with a freshly signed id_token ->
// handoff -> redeem. The JWKS is fetched live from the fake platform, so a
// regression anywhere in the chain (state, verification, resolution, ticket,
// redeem) fails this test.
func TestFullLaunchToRedeemFlow(t *testing.T) {
	h, p, ids := newFlowHandler(t, false)
	ticket := completeLaunch(t, h, p)

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"`+ticket+`","tenant_id":7}`)
	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		UserID       string `json:"user_id"`
		ContextID    string `json:"context_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "at-flow", body.AccessToken)
	require.Equal(t, "rt-flow", body.RefreshToken)
	require.Equal(t, "weknora-user-1", body.UserID)
	require.Equal(t, "course-42", body.ContextID)

	// Step-3 email match resolved the account and bound the platform sub.
	bound, err := ids.GetByAuthorityAndUID(context.Background(), "lti:client-1", "platform-sub-uuid")
	require.NoError(t, err)
	require.NotNil(t, bound)
	require.Equal(t, "weknora-user-1", bound.UserID)
	require.Equal(t, "email", bound.ResolvedVia)

	// The ticket is single-use: a replay must fail with 409.
	w = redeemPost(t, h, "redeem-secret", `{"ticket":"`+ticket+`"}`)
	require.Equal(t, http.StatusConflict, w.Code)
}

// TestFullFlowNonMemberTenantRejected drives the same chain but redeems for a
// tenant the user does not belong to; the real minter maps the service's
// ErrMembershipNotFound to ErrNotTenantMember and the handler answers 403.
func TestFullFlowNonMemberTenantRejected(t *testing.T) {
	h, p, _ := newFlowHandler(t, false)
	ticket := completeLaunch(t, h, p)

	w := redeemPost(t, h, "redeem-secret", `{"ticket":"`+ticket+`","tenant_id":999}`)
	require.Equal(t, http.StatusForbidden, w.Code)
}

// TestFullLaunchToSelfHandoffFlow drives the same chain but delivers the
// session through the built-in browser handoff instead of the S2S redeem:
// launch -> GET /lti/handoff?ticket=... -> 302 /#lti_result=<base64url(JSON)>
// carrying the minted JWT pair. The SPA decodes this hash exactly like the
// OIDC callback payload.
func TestFullLaunchToSelfHandoffFlow(t *testing.T) {
	h, p, _ := newFlowHandler(t, true)
	ticket := completeLaunch(t, h, p)

	w := getHandoff(t, h, ticket)
	require.Equal(t, http.StatusFound, w.Code)
	loc, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/", loc.Path)
	// The hash payload shape is pinned by the unit-level handoff test
	// (TestHandoffDeliversSessionViaHash); here we only assert the channel
	// carried a result on the happy path.
	require.Contains(t, loc.Fragment, "lti_result=")
}

// TestFullLaunchSelfHandoffReplayRejected asserts the single-use semantics
// hold on the browser channel too: re-presenting the consumed ticket must
// redirect to an lti_error instead of minting a second session.
func TestFullLaunchSelfHandoffReplayRejected(t *testing.T) {
	h, p, _ := newFlowHandler(t, true)
	ticket := completeLaunch(t, h, p)

	w := getHandoff(t, h, ticket)
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "lti_result=")

	w = getHandoff(t, h, ticket)
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "lti_error=invalid_ticket")
}
