package lti

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/stretchr/testify/require"
)

func TestUserTokenMinterMapsNoDefaultWorkspace(t *testing.T) {
	m := NewUserTokenMinter(&stubUserService{err: service.ErrNoDefaultWorkspace})
	_, err := m.IssueDefault(context.Background(), "u1")
	require.ErrorIs(t, err, ErrNoWorkspace)
}
