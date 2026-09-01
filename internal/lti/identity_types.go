package lti

import "time"

// ExternalIdentity is the binding-table row mapping an external identifier
// (an LTI sub, or a directory uid such as a SIS id) to a WeKnora account.
type ExternalIdentity struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	UserID      string    `gorm:"type:varchar(36);index" json:"user_id"`
	Authority   string    `gorm:"type:varchar(512);uniqueIndex:idx_ext_ident_authority_uid" json:"authority"`
	ExternalUID string    `gorm:"type:varchar(512);uniqueIndex:idx_ext_ident_authority_uid" json:"external_uid"`
	ResolvedVia string    `gorm:"type:varchar(32)" json:"resolved_via"` // existing | sis | email | created
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// TableName keeps the binding table name stable across backends.
func (ExternalIdentity) TableName() string { return "user_external_identities" }

// sisAuthority is the namespaced authority for a directory (SIS) uid binding,
// scoped by the platform issuer so test and production instances never clash.
func sisAuthority(reg *Registration) string {
	return "sis:" + reg.Issuer
}
