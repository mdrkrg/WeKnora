package lti

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lti/ltitest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// newFlowHandler wires the real protocol stack over real HTTP: the verifier
// pulls the platform JWKS from ltitest's live endpoint, the ticket service is
// the real single-use store, and identity resolution runs the deterministic
// directory-key matcher against a pre-provisioned roster (the synthetic
// <sis>@users.lti.invalid account). selfHandoff enables the built-in browser
// handoff endpoint.
func newFlowHandler(t *testing.T, selfHandoff bool) (*Handler, *ltitest.Platform) {
	t.Helper()
	setupSSRFWhitelist(t)
	setupNonceEnv(t)
	p := ltitest.NewPlatform(t)

	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.JWKSURI = p.JWKSURL()
	reg.PublicKeyset = ""
	reg.DirectoryClaim = "sis_user_id"
	regs := &fakeRegistrationStore{regs: []*Registration{reg}}

	users := &fakeUserCatalog{byEmail: map[string]*types.User{
		"20240001@users.lti.invalid": {ID: "weknora-user-1"},
	}}

	deps := &handlerDeps{
		registrations: regs,
		tickets:       NewTicketService(&fakeTicketStore{}, 120*time.Second),
		verifier:      NewVerifier(NewKeysetResolver(regs, nil)),
		resolver:      newTestMatcher(users),
		minter:        NewUserTokenMinter(&stubUserService{}),
	}
	if selfHandoff {
		deps.cfg = selfHandoffConfig()
	}
	h := testLTIHandler(t, deps)
	return h, p
}

// completeLaunch drives the OIDC third-party initiation and the launch
// POST, returning the single-use ticket issued at the handoff.
func completeLaunch(t *testing.T, h *Handler, p *ltitest.Platform) string {
	return completeLaunchWithUID(t, h, p, "20240001")
}

// completeLaunchWithUID drives the same chain but with a custom sis_user_id
// claim value, so failure states (un-provisioned roster row) can be reached.
func completeLaunchWithUID(t *testing.T, h *Handler, p *ltitest.Platform, uid string) string {
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

	tok := ltiClaims(p, func(m jwt.MapClaims) {
		m["nonce"] = nonce
		m[ClaimCustom] = map[string]any{"sis_user_id": uid}
	})
	w = postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusFound, w.Code)
	loc, err = url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "https://app.example.com/api/auth/lti/handoff", loc.Scheme+"://"+loc.Host+loc.Path)
	ticket := loc.Query().Get("ticket")
	require.NotEmpty(t, ticket)
	return ticket
}

// TestFullLaunchRosterNotProvisionedRendersNotSynced drives the chain for a
// sis id the roster sweep has not provisioned: resolution must fail closed at
// launch (roster-not-synced page) rather than create an account or issue a
// ticket.
func TestFullLaunchRosterNotProvisionedRendersNotSynced(t *testing.T) {
	h, p := newFlowHandler(t, false)
	setupNonceEnv(t)
	state, err := SignNonceState("nonce-abc")
	require.NoError(t, err)
	tok := ltiClaims(p, func(m jwt.MapClaims) {
		m["nonce"] = "nonce-abc"
		m[ClaimCustom] = map[string]any{"sis_user_id": "99999999"}
	})
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "名单尚未同步")
}

// TestFullLaunchToSelfHandoffFlow drives the same chain but delivers the
// session through the built-in browser handoff instead of the S2S redeem:
// launch -> GET /lti/handoff?ticket=... -> 302 /#lti_result=<base64url(JSON)>
// carrying the minted JWT pair. The SPA decodes this hash exactly like the
// OIDC callback payload.
func TestFullLaunchToSelfHandoffFlow(t *testing.T) {
	h, p := newFlowHandler(t, true)
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
	h, p := newFlowHandler(t, true)
	ticket := completeLaunch(t, h, p)

	w := getHandoff(t, h, ticket)
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "lti_result=")

	w = getHandoff(t, h, ticket)
	require.Equal(t, http.StatusFound, w.Code)
	require.Contains(t, w.Header().Get("Location"), "lti_error=invalid_ticket")
}
