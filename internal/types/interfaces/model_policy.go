package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ModelPolicyService owns deployment-wide Provider approval, fixed ingestion
// model bindings, and the locked parser profile.
type ModelPolicyService interface {
	GetPolicy(ctx context.Context) (*types.ModelGovernancePolicy, error)

	ValidateModelForWrite(ctx context.Context, model *types.Model) error
	PrepareModelForRuntime(ctx context.Context, model *types.Model) (*types.Model, error)
	FilterModelsForCaller(ctx context.Context, models []*types.Model) []*types.Model

	ApplyKnowledgeBasePolicy(ctx context.Context, kb *types.KnowledgeBase) error
	ValidateProcessOverrides(
		ctx context.Context,
		kb *types.KnowledgeBase,
		overrides *types.KnowledgeProcessOverrides,
		fileTypes []string,
	) error
	ApplyEffectiveProcessPolicy(ctx context.Context, eff *types.EffectiveProcessConfig) error
}
