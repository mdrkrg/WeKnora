package lti

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/lti/ltitest"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestLaunchIdentityNotFoundRendersNotSyncedPage(t *testing.T) {
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
		resolver: &fakeResolver{err: ErrIdentityNotFound},
	})
	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "名单尚未同步")
}

func TestLaunchIdentityMisconfiguredRendersConfigErrorPage(t *testing.T) {
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
		resolver: &fakeResolver{err: ErrIdentityMisconfigured},
	})
	tok := ltiClaims(p, func(m jwt.MapClaims) { m["nonce"] = "nonce-abc" })
	w := postLaunch(t, h, tok, state)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "服务配置错误")
}
