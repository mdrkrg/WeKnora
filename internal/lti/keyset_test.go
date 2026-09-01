package lti

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/Tencent/WeKnora/internal/lti/ltitest"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

// keyfuncVerifies reports whether the given keyfunc accepts a signed token.
func keyfuncVerifies(t *testing.T, kf keyfunc.Keyfunc, raw string) bool {
	t.Helper()
	_, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected alg %q", t.Method.Alg())
		}
		return kf.Keyfunc(t)
	}, jwt.WithValidMethods([]string{"RS256"}))
	return err == nil
}

func setupSSRFWhitelist(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
}

func TestKeysetResolverFetchesFromURL(t *testing.T) {
	setupSSRFWhitelist(t)
	p := ltitest.NewPlatform(t)
	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.JWKSURI = p.JWKSURL()
	reg.PublicKeyset = ""
	regs := &fakeRegistrationStore{regs: []*Registration{reg}}

	v := NewVerifier(NewKeysetResolver(regs, nil))
	got, err := v.Verify(context.Background(), ltiClaims(p, nil), reg)
	require.NoError(t, err)
	require.Equal(t, "platform-sub-uuid", got.Sub)
	require.Len(t, regs.savedKeyset, 1)
	require.NotEmpty(t, reg.PublicKeyset)
}

func TestKeysetResolverUsesCachedKeyset(t *testing.T) {
	p := ltitest.NewPlatform(t)
	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.PublicKeyset = p.PublicKeysetJSON()
	reg.JWKSURI = ""
	regs := &fakeRegistrationStore{regs: []*Registration{reg}}

	v := NewVerifier(NewKeysetResolver(regs, nil))
	got, err := v.Verify(context.Background(), ltiClaims(p, nil), reg)
	require.NoError(t, err)
	require.Equal(t, "platform-sub-uuid", got.Sub)
	require.Empty(t, regs.savedKeyset)
}

func TestKeysetResolverRefreshPersists(t *testing.T) {
	setupSSRFWhitelist(t)
	p := ltitest.NewPlatform(t)
	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.JWKSURI = p.JWKSURL()
	reg.PublicKeyset = ""
	regs := &fakeRegistrationStore{regs: []*Registration{reg}}

	r := NewKeysetResolver(regs, nil)

	kf, err := r.Refresh(context.Background(), reg)
	require.NoError(t, err)
	require.Len(t, regs.savedKeyset, 1)

	// Cached afterwards: no additional fetch.
	_, err = r.Resolve(context.Background(), reg)
	require.NoError(t, err)
	require.Len(t, regs.savedKeyset, 1)

	// The refreshed keyfunc actually verifies platform-signed tokens.
	require.True(t, keyfuncVerifies(t, kf, ltiClaims(p, nil)))
}

func TestKeysetResolverRejectsUnsafeScheme(t *testing.T) {
	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.JWKSURI = "file:///etc/passwd"
	regs := &fakeRegistrationStore{regs: []*Registration{reg}}

	v := NewVerifier(NewKeysetResolver(regs, nil))
	_, err := v.Verify(context.Background(), "not-a-token", reg)
	require.Error(t, err)
}

func TestKeysetResolverRejectsMalformedResponse(t *testing.T) {
	setupSSRFWhitelist(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.JWKSURI = srv.URL
	reg.PublicKeyset = ""
	regs := &fakeRegistrationStore{regs: []*Registration{reg}}

	v := NewVerifier(NewKeysetResolver(regs, nil))
	_, err := v.Verify(context.Background(), "not-a-token", reg)
	require.Error(t, err)
	require.Empty(t, regs.savedKeyset)
}
