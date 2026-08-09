package service

import (
	"context"
	"fmt"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// InitializeWorkspaceProvisioning preflights and reconciles deployment-owned
// providers and the governance policy. All semantic validation happens before
// the first write so an invalid enabled manifest cannot partially update the
// platform catalog. Builtin models themselves stay under the upstream
// builtin_models.yaml lifecycle; this manifest only references them.
func InitializeWorkspaceProvisioning(
	ctx context.Context,
	modelRepo interfaces.ModelRepository,
	modelPolicy interfaces.ModelPolicyService,
	cfg *types.WorkspaceProvisioningConfig,
) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if modelRepo == nil || modelPolicy == nil {
		return fmt.Errorf("workspace provisioning dependencies are unavailable")
	}

	configuredProviders, err := preflightWorkspaceProvisioning(ctx, modelRepo, modelPolicy, cfg)
	if err != nil {
		return err
	}
	if err := reconcileWorkspaceProviders(ctx, modelPolicy, configuredProviders); err != nil {
		return fmt.Errorf("reconcile workspace provisioning providers: %w", err)
	}
	return nil
}

type workspaceProviderCatalogReconciler interface {
	replacePlatformProviders(context.Context, []types.PlatformModelProvider) error
}

func preflightWorkspaceProvisioning(
	ctx context.Context,
	modelRepo interfaces.ModelRepository,
	modelPolicy interfaces.ModelPolicyService,
	cfg *types.WorkspaceProvisioningConfig,
) ([]types.PlatformModelProvider, error) {
	configuredProviders := make([]types.PlatformModelProvider, len(cfg.Providers))
	effectiveProviders := make(map[string]types.PlatformModelProvider, len(cfg.Providers))
	seenProviders := make(map[string]struct{}, len(cfg.Providers))
	for i := range cfg.Providers {
		item := cfg.Providers[i]
		if err := normalizePlatformProvider(&item); err != nil {
			return nil, fmt.Errorf("provider %d: %w", i, err)
		}
		if item.BaseURL != "" {
			if err := secutils.ValidateURLForSSRF(item.BaseURL); err != nil {
				return nil, fmt.Errorf("provider %q base URL is not allowed by SSRF policy", item.ID)
			}
		}
		if _, duplicate := seenProviders[item.ID]; duplicate {
			return nil, fmt.Errorf("duplicate platform provider id %q", item.ID)
		}
		seenProviders[item.ID] = struct{}{}
		item.ManagedBy = types.WorkspaceProvisioningManagedBy
		configuredProviders[i] = item
		// The enabled manifest is the complete provider catalog. Existing
		// runtime rows cannot override or supplement it.
		effectiveProviders[item.ID] = item
	}

	policy, err := modelPolicy.GetPolicy(ctx)
	if err != nil {
		return nil, fmt.Errorf("load model governance policy: %w", err)
	}

	defaultModels := []struct {
		purpose  string
		id       string
		expected types.ModelType
	}{
		{"chat", strings.TrimSpace(cfg.Defaults.ChatModelID), types.ModelTypeKnowledgeQA},
		{"embedding", strings.TrimSpace(cfg.Defaults.EmbeddingModelID), types.ModelTypeEmbedding},
		{"rerank", strings.TrimSpace(cfg.Defaults.RerankModelID), types.ModelTypeRerank},
	}
	for _, item := range defaultModels {
		if item.id == "" {
			return nil, fmt.Errorf("default %s model id is required", item.purpose)
		}
		model, err := modelRepo.GetByID(ctx, 0, item.id)
		if err != nil {
			return nil, fmt.Errorf("default %s model: %w", item.purpose, err)
		}
		if model == nil || !model.IsBuiltin || model.Status != types.ModelStatusActive {
			return nil, fmt.Errorf("default %s model is unavailable", item.purpose)
		}
		if model.Type != item.expected {
			return nil, fmt.Errorf("default %s model must have type %s", item.purpose, item.expected)
		}
		if item.expected == types.ModelTypeEmbedding && model.Parameters.EmbeddingParameters.Dimension <= 0 {
			return nil, fmt.Errorf("default embedding model dimension must be positive")
		}
		if err := validateProvisioningModelProvider(model, effectiveProviders, policy); err != nil {
			return nil, fmt.Errorf("default %s model is not compliant: %w", item.purpose, err)
		}
	}

	// Governance policy from the manifest must reference existing active
	// builtin models of the right type; type-level validation already ran
	// in normalizeAndValidate.
	if cfg.Policy != nil {
		policyBindings := []struct {
			field    string
			id       string
			expected types.ModelType
		}{
			{"fixed_ingest_embedding_id", strings.TrimSpace(cfg.Policy.FixedIngestEmbeddingID), types.ModelTypeEmbedding},
			{"fixed_ingest_summary_id", strings.TrimSpace(cfg.Policy.FixedIngestSummaryID), types.ModelTypeKnowledgeQA},
			{"fixed_ingest_vlm_id", strings.TrimSpace(cfg.Policy.FixedIngestVLMID), types.ModelTypeVLLM},
			{"fixed_ingest_asr_id", strings.TrimSpace(cfg.Policy.FixedIngestASRID), types.ModelTypeASR},
		}
		for _, binding := range policyBindings {
			if binding.id == "" {
				continue
			}
			model, err := modelRepo.GetByID(ctx, 0, binding.id)
			if err != nil {
				return nil, fmt.Errorf("policy %s: %w", binding.field, err)
			}
			if model == nil || !model.IsBuiltin || model.Status != types.ModelStatusActive {
				return nil, fmt.Errorf("policy %s must reference an active builtin model", binding.field)
			}
			if model.Type != binding.expected {
				return nil, fmt.Errorf("policy %s must reference a %s model", binding.field, binding.expected)
			}
		}
	}

	return configuredProviders, nil
}

func validateProvisioningModelProvider(
	model *types.Model,
	providers map[string]types.PlatformModelProvider,
	policy *types.ModelGovernancePolicy,
) error {
	if model == nil || model.Source == types.ModelSourceLocal {
		return nil
	}
	providerID := strings.ToLower(strings.TrimSpace(model.Parameters.Provider))
	if providerID == "" {
		return fmt.Errorf("remote model provider must be explicit")
	}
	profile, exists := providers[providerID]
	if !exists {
		return fmt.Errorf("provider %q is not in the platform catalog", providerID)
	}
	if policy != nil && policy.Mode == types.ModelPolicyModeEnforce && !profile.Approved {
		return fmt.Errorf("provider %q is not approved", providerID)
	}
	if !platformProviderSupportsType(profile, model.Type) {
		return fmt.Errorf("provider %q does not allow model type %s", providerID, model.Type)
	}
	if !profile.AllowCustomBaseURL && strings.TrimSpace(profile.BaseURL) != "" &&
		strings.TrimSpace(model.Parameters.BaseURL) != "" && !sameBaseURL(model.Parameters.BaseURL, profile.BaseURL) {
		return fmt.Errorf("provider %q base URL is locked by platform policy", providerID)
	}
	if !profile.AllowCustomBaseURL && strings.TrimSpace(profile.BaseURL) == "" &&
		strings.TrimSpace(model.Parameters.BaseURL) != "" {
		return fmt.Errorf("provider %q base URL is locked by platform policy", providerID)
	}
	effectiveBaseURL := strings.TrimSpace(model.Parameters.BaseURL)
	if !profile.AllowCustomBaseURL || effectiveBaseURL == "" {
		effectiveBaseURL = strings.TrimSpace(profile.BaseURL)
	}
	if effectiveBaseURL != "" {
		if err := secutils.ValidateURLForSSRF(effectiveBaseURL); err != nil {
			return fmt.Errorf("provider %q model URL is not allowed by SSRF policy", providerID)
		}
	}
	return nil
}

// validateWorkspaceDefaultModel verifies a manifest default reference: the
// model must exist, be active and of the right type (builtin when required),
// have a positive embedding dimension when checked, and pass runtime policy
// preparation. Failures return a typed ServiceUnavailable error safe to
// surface as 503; details go to the server log only.
func validateWorkspaceDefaultModel(
	ctx context.Context,
	modelRepo interfaces.ModelRepository,
	modelPolicy interfaces.ModelPolicyService,
	tenantID uint64,
	purpose string,
	id string,
	expected types.ModelType,
	requireBuiltin bool,
	checkEmbeddingDim bool,
) error {
	if modelRepo == nil || modelPolicy == nil {
		return apperrors.NewServiceUnavailableError("default " + purpose + " model is unavailable")
	}
	model, err := modelRepo.GetByID(ctx, tenantID, strings.TrimSpace(id))
	if err != nil {
		logger.Errorf(ctx, "[workspace-provisioning] default %s model lookup failed: %v", purpose, err)
		return apperrors.NewServiceUnavailableError("default " + purpose + " model is unavailable")
	}
	if model == nil || model.Status != types.ModelStatusActive || model.Type != expected ||
		(requireBuiltin && !model.IsBuiltin) {
		return apperrors.NewServiceUnavailableError("default " + purpose + " model is unavailable")
	}
	if checkEmbeddingDim && expected == types.ModelTypeEmbedding && model.Parameters.EmbeddingParameters.Dimension <= 0 {
		return apperrors.NewServiceUnavailableError("default embedding model is unavailable")
	}
	if _, err := modelPolicy.PrepareModelForRuntime(ctx, model); err != nil {
		logger.Errorf(ctx, "[workspace-provisioning] default %s model runtime validation failed: %v", purpose, err)
		return apperrors.NewServiceUnavailableError("default " + purpose + " model is unavailable")
	}
	return nil
}

// validateWorkspaceDefaultModels protects new workspace creation against
// configured defaults that became inactive or non-compliant after startup.
func validateWorkspaceDefaultModels(
	ctx context.Context,
	modelRepo interfaces.ModelRepository,
	modelPolicy interfaces.ModelPolicyService,
	cfg *types.WorkspaceProvisioningConfig,
) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	items := []struct {
		purpose  string
		id       string
		expected types.ModelType
	}{
		{"chat", cfg.Defaults.ChatModelID, types.ModelTypeKnowledgeQA},
		{"embedding", cfg.Defaults.EmbeddingModelID, types.ModelTypeEmbedding},
		{"rerank", cfg.Defaults.RerankModelID, types.ModelTypeRerank},
	}
	for _, item := range items {
		if err := validateWorkspaceDefaultModel(
			ctx, modelRepo, modelPolicy, 0, item.purpose, item.id, item.expected, true, true,
		); err != nil {
			return err
		}
	}
	return nil
}

func reconcileWorkspaceProviders(
	ctx context.Context,
	modelPolicy interfaces.ModelPolicyService,
	configured []types.PlatformModelProvider,
) error {
	catalog, ok := modelPolicy.(workspaceProviderCatalogReconciler)
	if !ok {
		return fmt.Errorf("model policy service does not support complete deployment provider reconciliation")
	}
	return catalog.replacePlatformProviders(ctx, configured)
}
