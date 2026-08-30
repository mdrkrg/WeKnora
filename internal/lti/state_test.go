package lti

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetNonceSecret() {
	ltiNonceSecretOnce = sync.Once{}
	ltiNonceSecret = nil
}

func TestSignVerifyNonceState(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-lti-nonce-secret")
	resetNonceSecret()
	defer resetNonceSecret()

	state, err := SignNonceState("nonce-abc")
	if err != nil {
		t.Fatalf("SignNonceState: %v", err)
	}
	got, err := VerifyNonceState(state, NonceStateMaxAge)
	if err != nil {
		t.Fatalf("VerifyNonceState: %v", err)
	}
	if got.Nonce != "nonce-abc" {
		t.Fatalf("nonce = %q, want nonce-abc", got.Nonce)
	}
	if got.IssuedAt == 0 {
		t.Fatal("issued_at not recorded")
	}
}

func TestVerifyNonceStateRejectsTampered(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-lti-nonce-secret")
	resetNonceSecret()
	defer resetNonceSecret()

	state, err := SignNonceState("nonce-abc")
	if err != nil {
		t.Fatalf("SignNonceState: %v", err)
	}
	parts := strings.Split(state, ".")
	if _, err := VerifyNonceState(parts[0]+".AAAA", NonceStateMaxAge); !errors.Is(err, ErrNonceStateSignature) {
		t.Fatalf("tampered state: err = %v, want ErrNonceStateSignature", err)
	}
	if _, err := VerifyNonceState("garbage", NonceStateMaxAge); !errors.Is(err, ErrNonceStateMalformed) {
		t.Fatalf("malformed state: err = %v, want ErrNonceStateMalformed", err)
	}
}

func TestVerifyNonceStateRejectsExpired(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-lti-nonce-secret")
	resetNonceSecret()
	defer resetNonceSecret()

	state, err := SignNonceState("nonce-abc")
	if err != nil {
		t.Fatalf("SignNonceState: %v", err)
	}
	if _, err := VerifyNonceState(state, time.Nanosecond); !errors.Is(err, ErrNonceStateExpired) {
		t.Fatalf("expired state: err = %v, want ErrNonceStateExpired", err)
	}
}
