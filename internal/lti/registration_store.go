package lti

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type registrationStore struct {
	db *gorm.DB
}

// NewRegistrationStore builds the GORM-backed registration store.
func NewRegistrationStore(db *gorm.DB) RegistrationStore {
	return &registrationStore{db: db}
}

func (s *registrationStore) GetByIssuerAndClientID(
	ctx context.Context, issuer, clientID string,
) (*Registration, error) {
	var reg Registration
	err := s.db.WithContext(ctx).
		Where("issuer = ? AND client_id = ?", issuer, clientID).
		First(&reg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &reg, nil
}

func (s *registrationStore) GetByID(ctx context.Context, id uint64) (*Registration, error) {
	var reg Registration
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&reg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &reg, nil
}

func (s *registrationStore) SaveKeyset(ctx context.Context, id uint64, raw string, fetchedAt time.Time) error {
	return s.db.WithContext(ctx).
		Model(&Registration{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"public_keyset":     raw,
			"keyset_fetched_at": fetchedAt,
		}).
		Error
}
