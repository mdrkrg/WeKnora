package lti

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type identityStore struct {
	db *gorm.DB
}

// NewIdentityStore builds the GORM-backed external-identity binding store.
func NewIdentityStore(db *gorm.DB) IdentityStore {
	return &identityStore{db: db}
}

func (s *identityStore) GetByAuthorityAndUID(
	ctx context.Context, authority, externalUID string,
) (*ExternalIdentity, error) {
	var id ExternalIdentity
	err := s.db.WithContext(ctx).
		Where("authority = ? AND external_uid = ?", authority, externalUID).
		First(&id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &id, nil
}

// Upsert is idempotent: it inserts or updates the row identified by
// (authority, external_uid), so a daemon re-push never duplicates a binding.
func (s *identityStore) Upsert(ctx context.Context, id *ExternalIdentity) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "authority"}, {Name: "external_uid"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "resolved_via", "last_seen_at"}),
	}).Create(id).Error
}

// Delete removes the binding row identified by (authority, externalUID),
// returning the number of rows removed (0 when nothing matched) so callers
// can distinguish a no-op from success.
func (s *identityStore) Delete(ctx context.Context, authority, externalUID string) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("authority = ? AND external_uid = ?", authority, externalUID).
		Delete(&ExternalIdentity{})
	return res.RowsAffected, res.Error
}
