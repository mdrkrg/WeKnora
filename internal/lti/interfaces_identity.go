package lti

import "context"

// IdentityStore persists external-identity bindings.
type IdentityStore interface {
	GetByAuthorityAndUID(ctx context.Context, authority, externalUID string) (*ExternalIdentity, error)
	Upsert(ctx context.Context, id *ExternalIdentity) error
}
