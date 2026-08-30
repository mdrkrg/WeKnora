// Package lti implements the LTI 1.3 tool side: OIDC third-party initiated
// login, launch (id_token verification), a JWKS endpoint for the tool's own
// signing key, and single-use tickets exchanged by frontends for a real
// session. It is intentionally free of deployment-specific concepts such as
// courses or workspace routing.
package lti

import "errors"

var (
	// ErrTicketNotFound is returned when a ticket hash is unknown.
	ErrTicketNotFound = errors.New("lti: ticket not found")
	// ErrTicketExpired is returned when a ticket exists but its TTL elapsed.
	ErrTicketExpired = errors.New("lti: ticket expired")
	// ErrTicketConsumed is returned when a single-use ticket was already used.
	ErrTicketConsumed = errors.New("lti: ticket already consumed")
	// ErrIdentityDisabled is returned by the placeholder identity resolver
	// until a real one is wired in.
	ErrIdentityDisabled = errors.New("lti: identity resolution not configured")
	// ErrNotTenantMember is returned by the token minter when a resolved user
	// has no membership in the requested tenant.
	ErrNotTenantMember = errors.New("lti: user is not a member of the requested tenant")
	// ErrNoWorkspace is returned by the token minter when a resolved user has
	// no home workspace, so a default-tenant issuance is impossible.
	ErrNoWorkspace = errors.New("lti: user has no default workspace")
	// ErrNonceStateMalformed is returned when a signed nonce state is
	// structurally invalid (bad shape, encoding, or missing nonce).
	ErrNonceStateMalformed = errors.New("lti: malformed nonce state")
	// ErrNonceStateSignature is returned when the HMAC over the state does
	// not match, i.e. the state was tampered with.
	ErrNonceStateSignature = errors.New("lti: invalid nonce state signature")
	// ErrNonceStateExpired is returned when the state is older than maxAge.
	ErrNonceStateExpired = errors.New("lti: nonce state expired")
	// ErrRegistrationNoKeyset is returned when a registration has neither a
	// JWKS URI nor a cached keyset to verify against.
	ErrRegistrationNoKeyset = errors.New("lti: registration has no jwks_uri and no cached keyset")
	// ErrUserServiceCapability is returned by the user-service adapters when
	// the wired service does not implement the LTI slice (UserCatalog /
	// IssueLTITokens), i.e. a deployment is missing LTI support.
	ErrUserServiceCapability = errors.New("lti: user service does not implement the LTI capability")
	// ErrIDTokenMissingSub is returned when an id_token lacks the sub claim,
	// which the LTI 1.3 spec marks as required.
	ErrIDTokenMissingSub = errors.New("lti: id_token missing sub")
	// ErrIDTokenMissingNonce is returned when an id_token lacks the nonce claim.
	ErrIDTokenMissingNonce = errors.New("lti: id_token missing nonce")
	// ErrIDTokenMissingMessageType is returned when an id_token lacks the message_type claim.
	ErrIDTokenMissingMessageType = errors.New("lti: id_token missing message_type")
	// ErrIDTokenMissingDeploymentID is returned when an id_token lacks the deployment_id claim.
	ErrIDTokenMissingDeploymentID = errors.New("lti: id_token missing deployment_id")
	// ErrTokenMinterDisabled is returned by the default token minter until a
	// real one is wired in.
	ErrTokenMinterDisabled = errors.New("lti: token minting not configured")
)
