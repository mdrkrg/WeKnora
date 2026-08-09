package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func provisioningTestModel(id string, modelType types.ModelType, dim int) *types.Model {
	return &types.Model{
		ID:        id,
		TenantID:  types.DefaultBuiltinModelTenantID,
		Name:      id,
		Type:      modelType,
		Source:    types.ModelSourceRemote,
		IsBuiltin: true,
		Status:    types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider:            "deployment-chat",
			EmbeddingParameters: types.EmbeddingParameters{Dimension: dim},
		},
	}
}

func workspaceProvisioningTestContext(t *testing.T) context.Context {
	t.Helper()
	t.Setenv("SSRF_WHITELIST_EXTRA", "model.invalid")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	return context.Background()
}

func workspaceProvisioningTestConfig() *types.WorkspaceProvisioningConfig {
	return &types.WorkspaceProvisioningConfig{
		Version: 1,
		Enabled: true,
		Providers: []types.PlatformModelProvider{
			{ID: "deployment-chat", Adapter: "generic", BaseURL: "https://model.invalid/v1", Approved: true, ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank}},
		},
		Defaults: types.WorkspaceProvisioningDefaults{
			ChatModelID:      "builtin-chat",
			EmbeddingModelID: "builtin-embedding",
			RerankModelID:    "builtin-rerank",
			ParserProfile:    &types.ParserProfile{ID: "deploy", Engine: "mineru", FileTypes: []string{"pdf"}},
		},
		Policy: &types.ModelGovernancePolicy{
			Mode:                    types.ModelPolicyModeEnforce,
			RequireExplicitProvider: true,
			FixedIngestEmbeddingID:  "builtin-embedding",
		},
	}
}

type workspaceProvisioningPolicyRecorder struct {
	interfaces.ModelPolicyService
	replaced []types.PlatformModelProvider
}

type workspaceProvisioningNonReconciler struct {
	interfaces.ModelPolicyService
}

func (s *workspaceProvisioningNonReconciler) GetPolicy(context.Context) (*types.ModelGovernancePolicy, error) {
	return &types.ModelGovernancePolicy{Mode: types.ModelPolicyModeOff}, nil
}

func (s *workspaceProvisioningNonReconciler) PrepareModelForRuntime(_ context.Context, model *types.Model) (*types.Model, error) {
	return model, nil
}

func (s *workspaceProvisioningPolicyRecorder) GetPolicy(context.Context) (*types.ModelGovernancePolicy, error) {
	return &types.ModelGovernancePolicy{Mode: types.ModelPolicyModeOff}, nil
}

func (s *workspaceProvisioningPolicyRecorder) PrepareModelForRuntime(_ context.Context, model *types.Model) (*types.Model, error) {
	return model, nil
}

func (s *workspaceProvisioningPolicyRecorder) replacePlatformProviders(_ context.Context, providers []types.PlatformModelProvider) error {
	s.replaced = append([]types.PlatformModelProvider(nil), providers...)
	return nil
}

func TestInitializeWorkspaceProvisioningDisabledIsNoOp(t *testing.T) {
	cfg := &types.WorkspaceProvisioningConfig{Version: 1}
	err := InitializeWorkspaceProvisioning(
		workspaceProvisioningTestContext(t), nil, nil, cfg,
	)
	require.NoError(t, err)
}

func TestInitializeWorkspaceProvisioningReconcilesProviders(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		"builtin-rerank":    provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	policy := &workspaceProvisioningPolicyRecorder{}

	err := InitializeWorkspaceProvisioning(workspaceProvisioningTestContext(t), modelRepo, policy, cfg)
	require.NoError(t, err)
	require.Len(t, policy.replaced, 1)
	assert.Equal(t, "deployment-chat", policy.replaced[0].ID)
	assert.Equal(t, types.WorkspaceProvisioningManagedBy, policy.replaced[0].ManagedBy)
}

func TestInitializeWorkspaceProvisioningRejectsMissingDefaultModel(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat": provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		// embedding-default missing
		"builtin-rerank": provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	policy := &workspaceProvisioningPolicyRecorder{}

	err := InitializeWorkspaceProvisioning(workspaceProvisioningTestContext(t), modelRepo, policy, cfg)
	require.ErrorContains(t, err, "default embedding model")
}

func TestInitializeWorkspaceProvisioningRejectsNonBuiltinDefault(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	model := provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0)
	model.IsBuiltin = false
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat":      model,
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		"builtin-rerank":    provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	policy := &workspaceProvisioningPolicyRecorder{}

	err := InitializeWorkspaceProvisioning(workspaceProvisioningTestContext(t), modelRepo, policy, cfg)
	require.ErrorContains(t, err, "default chat model is unavailable")
}

func TestInitializeWorkspaceProvisioningRejectsWrongDefaultType(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeEmbedding, 4096),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		"builtin-rerank":    provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	policy := &workspaceProvisioningPolicyRecorder{}

	err := InitializeWorkspaceProvisioning(workspaceProvisioningTestContext(t), modelRepo, policy, cfg)
	require.ErrorContains(t, err, "default chat model must have type")
}

func TestInitializeWorkspaceProvisioningRejectsPolicyBindingToMissingModel(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	cfg.Policy.FixedIngestEmbeddingID = "no-such-model"
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		"builtin-rerank":    provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	policy := &workspaceProvisioningPolicyRecorder{}

	err := InitializeWorkspaceProvisioning(workspaceProvisioningTestContext(t), modelRepo, policy, cfg)
	require.ErrorContains(t, err, "policy fixed_ingest_embedding_id")
}

func TestInitializeWorkspaceProvisioningRejectsDuplicateProviders(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	cfg.Providers = append(cfg.Providers, cfg.Providers[0])
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		"builtin-rerank":    provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	policy := &workspaceProvisioningPolicyRecorder{}

	err := InitializeWorkspaceProvisioning(workspaceProvisioningTestContext(t), modelRepo, policy, cfg)
	require.ErrorContains(t, err, "duplicate platform provider id")
}

func TestInitializeWorkspaceProvisioningReportsReconcileFailure(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		"builtin-rerank":    provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	policy := &workspaceProvisioningNonReconciler{}

	err := InitializeWorkspaceProvisioning(workspaceProvisioningTestContext(t), modelRepo, policy, cfg)
	require.ErrorContains(t, err, "does not support complete deployment provider reconciliation")
}

func TestGetPolicyPrefersManifestPolicyWhenEnabled(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{}},
		newPolicySettingsStub(map[string]any{settingModelPolicyMode: "off"}),
		cfg,
	).(*modelPolicyService)

	policy, err := svc.GetPolicy(policyContext())
	require.NoError(t, err)
	assert.Equal(t, types.ModelPolicyModeEnforce, policy.Mode)
	assert.Equal(t, "builtin-embedding", policy.FixedIngestEmbeddingID)
}

func TestGetPolicyFallsBackToSettingsWithoutManifest(t *testing.T) {
	cfg := &types.WorkspaceProvisioningConfig{Version: 1}
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{}},
		newPolicySettingsStub(map[string]any{settingModelPolicyMode: "enforce"}),
		cfg,
	).(*modelPolicyService)

	policy, err := svc.GetPolicy(policyContext())
	require.NoError(t, err)
	assert.Equal(t, types.ModelPolicyModeEnforce, policy.Mode)
}

func TestValidateWorkspaceDefaultModelsRejectsUnavailableReference(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	modelRepo := &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		// rerank missing
	}}
	policy := &workspaceProvisioningPolicyRecorder{}

	err := validateWorkspaceDefaultModels(context.Background(), modelRepo, policy, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default rerank model is unavailable")
}

func TestReplacePlatformProvidersWritesCompleteCatalog(t *testing.T) {
	settings := newPolicySettingsStub(map[string]any{})
	svc := NewModelPolicyService(
		&policyModelRepoStub{models: map[string]*types.Model{}},
		settings,
	).(*modelPolicyService)

	providers := []types.PlatformModelProvider{
		{ID: "z-provider", Adapter: "generic", Approved: true},
		{ID: "a-provider", Adapter: "generic", Approved: true},
	}
	require.NoError(t, svc.replacePlatformProviders(policyContext(), providers))

	loaded, err := svc.loadProviderCatalog(policyContext())
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	assert.Equal(t, "a-provider", loaded[0].ID)
}

func provisioningEnabledSettingsStub() *policySettingsStub {
	return newPolicySettingsStub(map[string]any{
		settingModelPolicyMode: "off",
		settingProviderCatalog: `[{"id":"deployment-chat","display_name":"Deployment Chat","adapter":"generic","base_url":"https://model.invalid/v1","approved":true,"allow_custom_base_url":false,"model_types":["KnowledgeQA","Embedding","Rerank"]}]`,
	})
}

func TestWorkspaceProvisioningKnowledgeBasePolicyAppliesDefaultsThenFixings(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{
			"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
			"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		}},
		provisioningEnabledSettingsStub(),
		cfg,
	).(*modelPolicyService)

	kb := &types.KnowledgeBase{}
	// Creation path: default model fields AND default parser rules apply.
	err := svc.ApplyKnowledgeBasePolicy(types.WithKnowledgeBaseCreationDefaults(policyContext()), kb)
	require.NoError(t, err)
	assert.Equal(t, "builtin-chat", kb.SummaryModelID)
	assert.Equal(t, "builtin-embedding", kb.EmbeddingModelID)
	require.NotEmpty(t, kb.ChunkingConfig.ParserEngineRules)
	assert.Equal(t, "mineru", kb.ChunkingConfig.ParserEngineRules[0].Engine)
}

func TestWorkspaceProvisioningKnowledgeBasePolicySkipsParserDefaultsOnUpdate(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{
			"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
			"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		}},
		provisioningEnabledSettingsStub(),
		cfg,
	).(*modelPolicyService)

	kb := &types.KnowledgeBase{}
	// Update path (no creation marker): model defaults still fill empty
	// fields, but default parser rules are NOT appended — pre-existing KBs
	// must not silently converge to the deployment parser.
	err := svc.ApplyKnowledgeBasePolicy(policyContext(), kb)
	require.NoError(t, err)
	assert.Equal(t, "builtin-chat", kb.SummaryModelID)
	assert.Empty(t, kb.ChunkingConfig.ParserEngineRules)
}

func TestWorkspaceProvisioningKnowledgeBasePolicyKeepsExplicitChoices(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{
			"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
			"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		}},
		provisioningEnabledSettingsStub(),
		cfg,
	).(*modelPolicyService)

	// Explicit choices are preserved unless they conflict with enforce-mode
	// fixed bindings; here no fixed bindings are configured.
	cfg.Policy.FixedIngestEmbeddingID = ""
	explicitChat := provisioningTestModel("explicit-chat", types.ModelTypeKnowledgeQA, 0)
	explicitEmbed := provisioningTestModel("explicit-embed", types.ModelTypeEmbedding, 4096)
	svc.repo = &policyModelRepoStub{models: map[string]*types.Model{
		"explicit-chat":     explicitChat,
		"explicit-embed":    explicitEmbed,
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
	}}
	kb := &types.KnowledgeBase{SummaryModelID: "explicit-chat", EmbeddingModelID: "explicit-embed"}
	err := svc.ApplyKnowledgeBasePolicy(policyContext(), kb)
	require.NoError(t, err)
	assert.Equal(t, "explicit-chat", kb.SummaryModelID)
	assert.Equal(t, "explicit-embed", kb.EmbeddingModelID)
}

func TestWorkspaceProvisioningKnowledgeBasePolicyRejectsUnavailableDefault(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{
			"builtin-chat": provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
			// embedding default missing from repo
		}},
		provisioningEnabledSettingsStub(),
		cfg,
	).(*modelPolicyService)

	kb := &types.KnowledgeBase{}
	cfg.Policy.FixedIngestEmbeddingID = ""
	// The default chat model is missing from the repo -> 503 from
	// validateWorkspaceDefaultReference (chat validation does not depend on
	// NeedsEmbeddingModel).
	svc.repo = &policyModelRepoStub{models: map[string]*types.Model{
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		"builtin-rerank":    provisioningTestModel("builtin-rerank", types.ModelTypeRerank, 0),
	}}
	err := svc.ApplyKnowledgeBasePolicy(policyContext(), kb)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default chat model is unavailable")
}

func TestWorkspaceProvisioningKnowledgeBasePolicyConvergesStoredModelOnUpdate(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{
			"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
			"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		}},
		provisioningEnabledSettingsStub(),
		cfg,
	).(*modelPolicyService)

	// Update path of a pre-policy KB: the stored embedding model differs from
	// the fixed binding; enforce must converge instead of rejecting the save.
	// The manifest fixes only the embedding binding, so the summary model is
	// left untouched (no binding to converge to).
	legacyChat := provisioningTestModel("legacy-chat", types.ModelTypeKnowledgeQA, 0)
	legacyEmbed := provisioningTestModel("legacy-embed", types.ModelTypeEmbedding, 4096)
	svc.repo = &policyModelRepoStub{models: map[string]*types.Model{
		"legacy-chat":       legacyChat,
		"legacy-embed":      legacyEmbed,
		"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
		"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
	}}
	kb := &types.KnowledgeBase{ID: "kb-pre-policy", SummaryModelID: "legacy-chat", EmbeddingModelID: "legacy-embed"}
	err := svc.ApplyKnowledgeBasePolicy(policyContext(), kb)
	require.NoError(t, err)
	assert.Equal(t, "legacy-chat", kb.SummaryModelID)
	assert.Equal(t, "builtin-embedding", kb.EmbeddingModelID)
}

func TestWorkspaceProvisioningKnowledgeBasePolicyRejectsExplicitConflictOnCreate(t *testing.T) {
	cfg := workspaceProvisioningTestConfig()
	svc := NewModelPolicyServiceWithWorkspaceProvisioning(
		&policyModelRepoStub{models: map[string]*types.Model{
			"builtin-chat":      provisioningTestModel("builtin-chat", types.ModelTypeKnowledgeQA, 0),
			"builtin-embedding": provisioningTestModel("builtin-embedding", types.ModelTypeEmbedding, 4096),
		}},
		provisioningEnabledSettingsStub(),
		cfg,
	).(*modelPolicyService)

	// Creation path keeps fail-closed semantics: an explicit model that
	// conflicts with the fixed binding is rejected.
	kb := &types.KnowledgeBase{SummaryModelID: "other-chat", EmbeddingModelID: "other-embed"}
	err := svc.ApplyKnowledgeBasePolicy(types.WithKnowledgeBaseCreationDefaults(policyContext()), kb)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixed by platform policy")
}
