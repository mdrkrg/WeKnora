package lti

import (
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
)

// LTI 1.3 claim URIs as emitted by IMS-standard platforms.
const (
	ClaimMessageType  = "https://purl.imsglobal.org/spec/lti/claim/message_type"
	ClaimDeploymentID = "https://purl.imsglobal.org/spec/lti/claim/deployment_id"
	ClaimContext      = "https://purl.imsglobal.org/spec/lti/claim/context"
	ClaimRoles        = "https://purl.imsglobal.org/spec/lti/claim/roles"
	ClaimCustom       = "https://purl.imsglobal.org/spec/lti/claim/custom"
)

// Registration describes one LTI 1.3 platform as configured in the tool.
// Rows are seeded by operators via SQL or env.
type Registration struct {
	ID       uint64 `gorm:"primaryKey" json:"id"`
	Issuer   string `gorm:"type:varchar(512);uniqueIndex:idx_lti_reg_iss_client" json:"iss"`
	ClientID string `gorm:"type:varchar(256);uniqueIndex:idx_lti_reg_iss_client" json:"client_id"`
	// DeploymentIDs is a JSON array of allowed deployment ids; empty means any deployment is allowed.
	DeploymentIDs   string     `gorm:"type:text" json:"deployment_ids"`
	AuthEndpoint    string     `gorm:"type:text" json:"auth_endpoint"`
	JWKSURI         string     `gorm:"type:text" json:"jwks_uri"`
	PublicKeyset    string     `gorm:"type:text" json:"public_keyset"` // cached JWKS JSON
	KeysetFetchedAt *time.Time `json:"keyset_fetched_at"`
	// DirectoryClaim is the key inside the LTI custom claim carrying the directory uid.
	DirectoryClaim string    `gorm:"type:varchar(128)" json:"directory_claim"`
	Enabled        bool      `gorm:"default:true" json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TableName keeps the registration table name stable across backends.
func (Registration) TableName() string { return "lti_registrations" }

// ToolKey is the tool's own signing key pair, published at
// /.well-known/jwks.json so the platform can register the tool. The private
// key is encrypted at rest with SYSTEM_AES_KEY.
type ToolKey struct {
	KID        string    `gorm:"primaryKey;type:varchar(128)" json:"kid"`
	PrivateKey string    `gorm:"type:text" json:"-"` // enc:v1: ...
	PublicJWK  string    `gorm:"type:text" json:"public_jwk"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName keeps the tool key table name stable across backends.
func (ToolKey) TableName() string { return "lti_tool_keys" }

// BeforeSave encrypts the private key at rest unless already encrypted.
func (k *ToolKey) BeforeSave(*gorm.DB) error {
	if k.PrivateKey == "" || strings.HasPrefix(k.PrivateKey, utils.EncPrefix) {
		return nil
	}
	enc, err := utils.EncryptAESGCM(k.PrivateKey, utils.GetAESKey())
	if err != nil {
		return err
	}
	k.PrivateKey = enc
	return nil
}

// AfterFind decrypts the private key for in-memory use.
func (k *ToolKey) AfterFind(*gorm.DB) error {
	if plaintext, ok := utils.DecryptStoredSecretLenient(k.PrivateKey); ok {
		k.PrivateKey = plaintext
	}
	return nil
}

// Ticket is a single-use, short-lived credential minted after a successful
// launch and exchanged by the web app for a real session.
type Ticket struct {
	TokenHash  string     `gorm:"primaryKey;type:varchar(64)" json:"-"`
	UserID     string     `gorm:"type:varchar(36);index" json:"user_id"`
	ContextID  string     `gorm:"type:varchar(256)" json:"context_id,omitempty"`
	Roles      string     `gorm:"type:text" json:"roles,omitempty"` // JSON array of raw LTI role URIs
	ExpiresAt  time.Time  `json:"expires_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TableName keeps the ticket table name stable across backends.
func (Ticket) TableName() string { return "lti_tickets" }

// LaunchIdentity is the identity material the launch handler hands to the
// IdentityResolver.
type LaunchIdentity struct {
	RegistrationID uint64
	Sub            string
	Email          string
	DirectoryUID   string
	Roles          []string
}

// IdentityResolution is what the IdentityResolver answers with: the WeKnora
// account the launch maps to.
type IdentityResolution struct {
	UserID string
}

// TokenResult is the JWT pair minted for a resolved user.
type TokenResult struct {
	AccessToken  string
	RefreshToken string
}

// VerifiedToken carries the claims of a verified id_token.
type VerifiedToken struct {
	Sub          string
	Issuer       string
	Audience     string
	Nonce        string
	MessageType  string
	DeploymentID string
	ContextID    string
	Roles        []string
	Email        string
	DirectoryUID string
}
