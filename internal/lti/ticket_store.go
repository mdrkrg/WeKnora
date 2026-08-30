package lti

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type ticketStore struct {
	db *gorm.DB
}

// NewTicketStore builds the GORM-backed ticket store.
func NewTicketStore(db *gorm.DB) TicketStore {
	return &ticketStore{db: db}
}

func (s *ticketStore) Create(ctx context.Context, t *Ticket) error {
	return s.db.WithContext(ctx).Create(t).Error
}

// Consume atomically claims a single-use ticket: only an unconsumed,
// unexpired row flips consumed_at, so a concurrent double redeem loses the
// race and gets ErrTicketConsumed.
func (s *ticketStore) Consume(ctx context.Context, tokenHash string) (*Ticket, error) {
	var t Ticket
	err := s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTicketNotFound
		}
		return nil, err
	}
	if t.ConsumedAt != nil {
		return nil, ErrTicketConsumed
	}
	if time.Now().After(t.ExpiresAt) {
		return nil, ErrTicketExpired
	}
	now := time.Now()
	res := s.db.WithContext(ctx).Model(&Ticket{}).
		Where("token_hash = ? AND consumed_at IS NULL", tokenHash).
		Update("consumed_at", now)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrTicketConsumed
	}
	t.ConsumedAt = &now
	return &t, nil
}

func (s *ticketStore) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("expires_at < ?", cutoff).
		Delete(&Ticket{})
	return res.RowsAffected, res.Error
}
