package lti

import (
	"context"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/Tencent/WeKnora/internal/lti/ltitest"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func baseRegistration(issuer, clientID string) *Registration {
	return &Registration{
		ID:            1,
		Issuer:        issuer,
		ClientID:      clientID,
		DeploymentIDs: `["d1"]`,
		AuthEndpoint:  "https://platform.example.com/authorize",
		Enabled:       true,
	}
}

func ltiClaims(p *ltitest.Platform, mut func(jwt.MapClaims)) string {
	claims := jwt.MapClaims{
		"iss":             "https://platform.example.com",
		"aud":             "client-1",
		"iat":             time.Now().Unix(),
		"sub":             "platform-sub-uuid",
		"nonce":           "nonce-abc",
		"email":           "student@example.com",
		ClaimMessageType:  "LtiResourceLinkRequest",
		ClaimDeploymentID: "d1",
		ClaimContext: map[string]any{
			"id": "course-42",
		},
		ClaimRoles: []string{
			"http://purl.imsglobal.org/vocab/lis/v2/membership#Learner",
		},
		ClaimCustom: map[string]any{
			"sis_user_id": "20240001",
		},
	}
	if mut != nil {
		mut(claims)
	}
	return p.Sign(claims)
}

func TestVerifyValidToken(t *testing.T) {
	p := ltitest.NewPlatform(t)
	kf, err := p.Keyfunc()
	require.NoError(t, err)

	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.DirectoryClaim = "sis_user_id"
	v := NewVerifier(&fakeKeysets{kf: kf})

	got, err := v.Verify(context.Background(), ltiClaims(p, nil), reg)
	require.NoError(t, err)
	require.Equal(t, "platform-sub-uuid", got.Sub)
	require.Equal(t, "https://platform.example.com", got.Issuer)
	require.Equal(t, "course-42", got.ContextID)
	require.Equal(t, "d1", got.DeploymentID)
	require.Equal(t, "LtiResourceLinkRequest", got.MessageType)
	require.Equal(t, "nonce-abc", got.Nonce)
	require.Equal(t, "student@example.com", got.Email)
	require.Equal(t, "20240001", got.DirectoryUID)
	require.Len(t, got.Roles, 1)
}

func TestVerifyRejectsMissingNonce(t *testing.T) {
	p := ltitest.NewPlatform(t)
	kf, err := p.Keyfunc()
	require.NoError(t, err)
	v := NewVerifier(&fakeKeysets{kf: kf})
	reg := baseRegistration("https://platform.example.com", "client-1")

	tok := ltiClaims(p, func(m jwt.MapClaims) {
		delete(m, "nonce")
	})
	_, err = v.Verify(context.Background(), tok, reg)
	require.ErrorIs(t, err, ErrIDTokenMissingNonce)
}

func TestVerifyRejectsDeploymentNotAllowed(t *testing.T) {
	p := ltitest.NewPlatform(t)
	kf, err := p.Keyfunc()
	require.NoError(t, err)
	v := NewVerifier(&fakeKeysets{kf: kf})
	reg := baseRegistration("https://platform.example.com", "client-1")

	tok := ltiClaims(p, func(m jwt.MapClaims) {
		m[ClaimDeploymentID] = "d-unknown"
	})
	_, err = v.Verify(context.Background(), tok, reg)
	require.Error(t, err)
}

func TestVerifyAllowsAnyDeploymentWhenListEmpty(t *testing.T) {
	p := ltitest.NewPlatform(t)
	kf, err := p.Keyfunc()
	require.NoError(t, err)
	reg := baseRegistration("https://platform.example.com", "client-1")
	reg.DeploymentIDs = ""
	v := NewVerifier(&fakeKeysets{kf: kf})

	tok := ltiClaims(p, func(m jwt.MapClaims) {
		m[ClaimDeploymentID] = "d-anything"
	})
	_, err = v.Verify(context.Background(), tok, reg)
	require.NoError(t, err)
}

// The manual RS256 check duplicates jwt.WithValidMethods; kept as defense in depth.
func TestVerifyRejectsWrongAlgorithm(t *testing.T) {
	p := ltitest.NewPlatform(t)
	kf, err := p.Keyfunc()
	require.NoError(t, err)
	v := NewVerifier(&fakeKeysets{kf: kf})
	reg := baseRegistration("https://platform.example.com", "client-1")

	claims := jwt.MapClaims{
		"iss": "https://platform.example.com",
		"aud": "client-1",
		"exp": time.Now().Add(time.Minute).Unix(),
		"sub": "platform-sub-uuid",
	}
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok, err := hs.SignedString([]byte("weak-secret"))
	require.NoError(t, err)

	_, err = v.Verify(context.Background(), tok, reg)
	require.Error(t, err)
}

// swapKeysets starts with a wrong keyfunc on Resolve (unknown kid) and swaps
// in the right one on Refresh, exercising the refresh-on-unknown-kid retry.
type swapKeysets struct {
	kf           keyfunc.Keyfunc
	right        keyfunc.Keyfunc
	refreshCalls int
}

func (s *swapKeysets) Resolve(context.Context, *Registration) (keyfunc.Keyfunc, error) {
	return s.kf, nil
}

func (s *swapKeysets) Refresh(context.Context, *Registration) (keyfunc.Keyfunc, error) {
	s.refreshCalls++
	s.kf = s.right
	return s.kf, nil
}

func TestVerifyRefreshesOnUnknownKid(t *testing.T) {
	p := ltitest.NewPlatform(t)
	// The wrong platform uses a different kid, so the first parse fails with
	// "unknown kid" rather than a signature mismatch.
	wrongKF, err := ltitest.NewPlatformWithKID(t, "wrong-kid").Keyfunc()
	require.NoError(t, err)
	rightKF, err := p.Keyfunc()
	require.NoError(t, err)

	swap := &swapKeysets{kf: wrongKF, right: rightKF}
	v := NewVerifier(swap)
	reg := baseRegistration("https://platform.example.com", "client-1")

	got, err := v.Verify(context.Background(), ltiClaims(p, nil), reg)
	require.NoError(t, err)
	require.Equal(t, "platform-sub-uuid", got.Sub)
	require.Equal(t, 1, swap.refreshCalls)
}
