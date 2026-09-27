package lti

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/MicahParks/jwkset"
	"gorm.io/gorm"
)

// toolKeyBits is the RSA modulus size for the tool's signing key pair.
const toolKeyBits = 2048

type toolKeyStore struct {
	db *gorm.DB
}

// NewToolKeyStore builds the tool-key store. The key pair is generated lazily
// on first use and the private key is encrypted at rest via the ToolKey
// GORM hooks (SYSTEM_AES_KEY).
func NewToolKeyStore(db *gorm.DB) ToolKeyStore {
	return &toolKeyStore{db: db}
}

func (s *toolKeyStore) Ensure(ctx context.Context) (*ToolKey, error) {
	var key ToolKey
	err := s.db.WithContext(ctx).Order("created_at ASC").First(&key).Error
	if err == nil {
		return &key, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return s.create(ctx)
}

func (s *toolKeyStore) create(ctx context.Context) (*ToolKey, error) {
	priv, err := rsa.GenerateKey(rand.Reader, toolKeyBits)
	if err != nil {
		return nil, fmt.Errorf("lti: generate tool key: %w", err)
	}
	kid, err := randomKID()
	if err != nil {
		return nil, err
	}
	publicJWK, err := marshalPublicJWK(&priv.PublicKey, kid)
	if err != nil {
		return nil, err
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	key := &ToolKey{KID: kid, PrivateKey: string(privatePEM), PublicJWK: publicJWK}
	if err := s.db.WithContext(ctx).Create(key).Error; err != nil {
		return nil, fmt.Errorf("lti: persist tool key: %w", err)
	}
	return key, nil
}

func randomKID() (string, error) {
	return randomBase64URLBytes(16)
}

// marshalPublicJWK renders a single public JWK JSON object (kty/kid/n/e).
func marshalPublicJWK(pub *rsa.PublicKey, kid string) (string, error) {
	jwk, err := jwkset.NewJWKFromKey(pub, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{KID: kid, USE: jwkset.UseSig, ALG: jwkset.AlgRS256},
		Validate: jwkset.JWKValidateOptions{SkipAll: true},
	})
	if err != nil {
		return "", fmt.Errorf("lti: marshal tool public key: %w", err)
	}
	raw, err := json.Marshal(jwk.Marshal())
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
