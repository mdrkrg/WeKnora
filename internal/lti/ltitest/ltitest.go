// Package ltitest provides a minimal fake LTI 1.3 platform for tests: an RSA
// signing key, a live JWKS endpoint, and helpers to sign id_tokens the way a
// real platform would.
package ltitest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Platform is a fake LTI 1.3 platform.
type Platform struct {
	t   *testing.T
	key *rsa.PrivateKey
	kid string
	srv *httptest.Server
}

// NewPlatform starts a fake platform serving its public keyset over a live
// JWKS endpoint. The returned value is cleaned up automatically.
func NewPlatform(t *testing.T) *Platform {
	return NewPlatformWithKID(t, "test-platform-kid")
}

// NewPlatformWithKID is NewPlatform with a caller-chosen key id, useful for
// rotating/unknown-kid scenarios.
func NewPlatformWithKID(t *testing.T, kid string) *Platform {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ltitest: generate platform key: %v", err)
	}
	p := &Platform{t: t, key: key, kid: kid}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storage := jwkset.NewMemoryStorage()
		if err := storage.KeyWrite(r.Context(), p.publicJWK()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw, err := storage.JSONPublic(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(raw)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *Platform) publicJWK() jwkset.JWK {
	jwk, err := jwkset.NewJWKFromKey(&p.key.PublicKey, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{KID: p.kid, USE: jwkset.UseSig},
		Validate: jwkset.JWKValidateOptions{SkipAll: true},
	})
	if err != nil {
		p.t.Fatalf("ltitest: build public jwk: %v", err)
	}
	return jwk
}

// JWKSURL returns the platform's live JWKS endpoint.
func (p *Platform) JWKSURL() string { return p.srv.URL + "/.well-known/jwks.json" }

// PublicKeysetJSON returns the platform's public keyset document, useful for
// seeding a registration's cached keyset.
func (p *Platform) PublicKeysetJSON() string {
	storage := jwkset.NewMemoryStorage()
	if err := storage.KeyWrite(context.Background(), p.publicJWK()); err != nil {
		p.t.Fatalf("ltitest: write keyset: %v", err)
	}
	raw, err := storage.JSONPublic(context.Background())
	if err != nil {
		p.t.Fatalf("ltitest: marshal keyset: %v", err)
	}
	return string(raw)
}

// Keyfunc builds a keyfunc backed by the platform's live JWKS endpoint.
func (p *Platform) Keyfunc() (keyfunc.Keyfunc, error) {
	return keyfunc.NewDefaultCtx(context.Background(), []string{p.JWKSURL()})
}

// Sign mints an RS256 id_token carrying the given claims. exp defaults to ten
// minutes out when absent.
func (p *Platform) Sign(claims jwt.MapClaims) string {
	p.t.Helper()
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(10 * time.Minute).Unix()
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = p.kid
	s, err := tok.SignedString(p.key)
	if err != nil {
		p.t.Fatalf("ltitest: sign token: %v", err)
	}
	return s
}

// PublicKey exposes the platform's public key for direct comparison.
func (p *Platform) PublicKey() *rsa.PublicKey { return &p.key.PublicKey }
