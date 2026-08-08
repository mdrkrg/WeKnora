package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// CanvasOAuthService manages workspace-level Canvas LMS OAuth app credentials.
type CanvasOAuthService interface {
	CheckStatus(ctx context.Context) (*types.CanvasOAuthStatusResult, error)
}
