package lti

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Verifier validates an id_token against a registration's key set and claims.
type Verifier struct {
	keysets KeysetResolver
}

// NewVerifier builds a verifier backed by the given keyset resolver.
func NewVerifier(keysets KeysetResolver) *Verifier {
	return &Verifier{keysets: keysets}
}

// Verify checks signature, issuer, audience, expiry, nonce and deployment
// against the registration, returning the trusted claims. When verification
// fails on a key-miss (e.g. rotation), the keyset is refreshed once and the
// token re-verified.
func (v *Verifier) Verify(ctx context.Context, rawToken string, reg *Registration) (*VerifiedToken, error) {
	return v.parse(ctx, rawToken, reg, false)
}

func (v *Verifier) parse(
	ctx context.Context, rawToken string, reg *Registration, retried bool,
) (*VerifiedToken, error) {
	kf, err := v.keysets.Resolve(ctx, reg)
	if err != nil {
		return nil, fmt.Errorf("lti: resolve keyset: %w", err)
	}
	var claims jwt.MapClaims
	_, err = jwt.ParseWithClaims(rawToken, &claims,
		func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
				return nil, fmt.Errorf("lti: unexpected signing method %q", t.Method.Alg())
			}
			return kf.Keyfunc(t)
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(reg.Issuer),
		jwt.WithAudience(reg.ClientID),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		if !retried {
			if _, rerr := v.keysets.Refresh(ctx, reg); rerr == nil {
				return v.parse(ctx, rawToken, reg, true)
			}
		}
		return nil, fmt.Errorf("lti: verify id_token: %w", err)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, ErrIDTokenMissingSub
	}
	nonce, _ := claims["nonce"].(string)
	if nonce == "" {
		return nil, ErrIDTokenMissingNonce
	}
	msgType, _ := claims[ClaimMessageType].(string)
	if msgType == "" {
		return nil, ErrIDTokenMissingMessageType
	}
	deployment, _ := claims[ClaimDeploymentID].(string)
	if deployment == "" {
		return nil, ErrIDTokenMissingDeploymentID
	}
	if err := checkDeploymentAllowed(reg.DeploymentIDs, deployment); err != nil {
		return nil, err
	}

	issuer, _ := claims["iss"].(string)
	email, _ := claims["email"].(string)
	return &VerifiedToken{
		Sub:          sub,
		Issuer:       issuer,
		Audience:     firstAudience(claims["aud"]),
		Nonce:        nonce,
		MessageType:  msgType,
		DeploymentID: deployment,
		ContextID:    contextIDFromClaims(claims),
		Roles:        rolesFromClaims(claims),
		Email:        email,
		DirectoryUID: directoryUIDFromClaims(reg, claims),
	}, nil
}

func checkDeploymentAllowed(rawList, deployment string) error {
	if strings.TrimSpace(rawList) == "" {
		return nil // empty list means any deployment is allowed
	}
	var ids []string
	if err := json.Unmarshal([]byte(rawList), &ids); err != nil {
		return fmt.Errorf("lti: invalid deployment_ids: %w", err)
	}
	for _, id := range ids {
		if id == deployment {
			return nil
		}
	}
	return fmt.Errorf("lti: deployment %q not allowed", deployment)
}

func firstAudience(v any) string {
	switch a := v.(type) {
	case string:
		return a
	case []any:
		if len(a) > 0 {
			if s, ok := a[0].(string); ok {
				return s
			}
		}
	case []string:
		if len(a) > 0 {
			return a[0]
		}
	}
	return ""
}

func contextIDFromClaims(claims jwt.MapClaims) string {
	ctxVal, ok := claims[ClaimContext].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := ctxVal["id"].(string)
	return id
}

func rolesFromClaims(claims jwt.MapClaims) []string {
	raw, ok := claims[ClaimRoles].([]any)
	if !ok {
		return nil
	}
	roles := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			roles = append(roles, s)
		}
	}
	return roles
}

func directoryUIDFromClaims(reg *Registration, claims jwt.MapClaims) string {
	if reg.DirectoryClaim == "" {
		return ""
	}
	custom, ok := claims[ClaimCustom].(map[string]any)
	if !ok {
		return ""
	}
	v, _ := custom[reg.DirectoryClaim].(string)
	return v
}
