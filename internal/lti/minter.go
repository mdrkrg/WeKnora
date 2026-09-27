package lti

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ltiTokenMinter is the narrow slice of the user service the LTI handoff
// needs. It is satisfied by *service.userService via a lazy type assertion.
type ltiTokenMinter interface {
	IssueLTITokens(ctx context.Context, userID string, tenantID uint64) (accessToken, refreshToken string, err error)
}

type userTokenMinter struct {
	us interfaces.UserService
}

// NewUserTokenMinter adapts the user service's LTI token issuance into the
// TokenMinter contract. The interface is asserted lazily at call time, so a
// service that stops exposing IssueLTITokens degrades to a per-request error
// instead of failing to boot.
func NewUserTokenMinter(us interfaces.UserService) TokenMinter {
	return &userTokenMinter{us: us}
}

func (m *userTokenMinter) svc() (ltiTokenMinter, error) {
	svc, ok := m.us.(ltiTokenMinter)
	if !ok {
		return nil, ErrUserServiceCapability
	}
	return svc, nil
}

func (m *userTokenMinter) IssueDefault(ctx context.Context, userID string) (*TokenResult, error) {
	svc, err := m.svc()
	if err != nil {
		return nil, err
	}
	access, refresh, err := svc.IssueLTITokens(ctx, userID, 0)
	if err != nil {
		if errors.Is(err, service.ErrNoDefaultWorkspace) {
			return nil, ErrNoWorkspace
		}
		return nil, err
	}
	return &TokenResult{AccessToken: access, RefreshToken: refresh}, nil
}
