package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/datasource/oauth"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type canvasOAuthService struct{}

// NewCanvasOAuthService constructs the deployment-level Canvas OAuth
// app credential status service.
func NewCanvasOAuthService() interfaces.CanvasOAuthService {
	return &canvasOAuthService{}
}

func (s *canvasOAuthService) CheckStatus(ctx context.Context) (*types.CanvasOAuthStatusResult, error) {
	_ = ctx

	app, err := oauth.LoadAppCredentials()
	if err != nil {
		return nil, fmt.Errorf("canvas_oauth invalid: %w", err)
	}
	if app == nil {
		return &types.CanvasOAuthStatusResult{Configured: false}, nil
	}

	return &types.CanvasOAuthStatusResult{
		Configured: true,
		BaseURL:    app.BaseURL,
		ClientID:   app.ClientID,
	}, nil
}
