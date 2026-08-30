package lti

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
)

// NonceStateMaxAge bounds how long a signed nonce state stays valid. The
// launch handler passes its own configured max age; this is the default when
// none is configured.
const NonceStateMaxAge = config.DefaultLTINonceMaxAge

var (
	ltiNonceSecretOnce sync.Once
	ltiNonceSecret     []byte
)

// nonceStateKey derives the HMAC key for signed nonce states from JWT_SECRET
// (falling back to a process-random key so local testing works without env).
func nonceStateKey() []byte {
	ltiNonceSecretOnce.Do(func() {
		if secret := strings.TrimSpace(os.Getenv("JWT_SECRET")); secret != "" {
			ltiNonceSecret = []byte(secret)
			return
		}
		buf := make([]byte, 32)
		_, _ = rand.Read(buf)
		ltiNonceSecret = buf
	})
	return ltiNonceSecret
}

// NonceState is the signed payload carried in the OIDC `state` parameter and
// echoed back by the platform on launch, tying the login initiation to the
// id_token nonce.
type NonceState struct {
	Nonce    string `json:"nonce"`
	IssuedAt int64  `json:"iat"`
}

// SignNonceState produces a base64url(payload).base64url(hmac) token.
func SignNonceState(nonce string) (string, error) {
	payload, err := json.Marshal(NonceState{Nonce: nonce, IssuedAt: time.Now().Unix()})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, nonceStateKey())
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyNonceState checks the HMAC signature and freshness of a signed state.
func VerifyNonceState(raw string, maxAge time.Duration) (*NonceState, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return nil, ErrNonceStateMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrNonceStateMalformed
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrNonceStateMalformed
	}
	mac := hmac.New(sha256.New, nonceStateKey())
	_, _ = mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), want) {
		return nil, ErrNonceStateSignature
	}
	var st NonceState
	if err := json.Unmarshal(payload, &st); err != nil {
		return nil, ErrNonceStateMalformed
	}
	if st.Nonce == "" {
		return nil, ErrNonceStateMalformed
	}
	issued := time.Unix(st.IssuedAt, 0)
	if maxAge > 0 && (time.Since(issued) > maxAge || time.Until(issued) > time.Minute) {
		return nil, ErrNonceStateExpired
	}
	return &st, nil
}
