package lti

import "context"

// IdentityStore persists external-identity bindings.
type IdentityStore interface {
	GetByAuthorityAndUID(ctx context.Context, authority, externalUID string) (*ExternalIdentity, error)
	Upsert(ctx context.Context, id *ExternalIdentity) error
	// Delete removes the binding row identified by (authority, externalUID),
	// returning the number of rows removed (0 when nothing matched).
	Delete(ctx context.Context, authority, externalUID string) (int64, error)
}
