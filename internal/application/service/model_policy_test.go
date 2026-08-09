package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type policySettingsStub struct {
	values map[string]any
}

func newPolicySettingsStub(values map[string]any) *policySettingsStub {
	return &policySettingsStub{values: values}
}

func (s *policySettingsStub) GetInt(_ context.Context, key, _ string, def int64) int64 {
	if value, ok := s.values[key].(int64); ok {
		return value
	}
	return def
}

func (s *policySettingsStub) GetString(_ context.Context, key, _ string, def string) string {
	if value, ok := s.values[key].(string); ok {
		return value
	}
	return def
}

func (s *policySettingsStub) GetBool(_ context.Context, key, _ string, def bool) bool {
	if value, ok := s.values[key].(bool); ok {
		return value
	}
	return def
}

func (s *policySettingsStub) GetStringList(_ context.Context, key, _ string, def []string) []string {
	if value, ok := s.values[key].([]string); ok {
		return value
	}
	return def
}

func (s *policySettingsStub) List(context.Context) ([]*types.SystemSetting, error) { return nil, nil }
func (s *policySettingsStub) Get(context.Context, string) (*types.SystemSetting, error) {
	return nil, nil
}

func (s *policySettingsStub) Update(_ context.Context, key string, value any) (*types.SystemSetting, error) {
	s.values[key] = value
	return &types.SystemSetting{Key: key}, nil
}

func (s *policySettingsStub) Reset(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}
func (s *policySettingsStub) SubscribeRedis(context.Context) error { return nil }

type policyModelRepoStub struct {
	interfaces.ModelRepository
	models map[string]*types.Model
}

func (s *policyModelRepoStub) Create(_ context.Context, model *types.Model) error {
	s.models[model.ID] = model
	return nil
}

func (s *policyModelRepoStub) GetByID(_ context.Context, _ uint64, id string) (*types.Model, error) {
	return s.models[id], nil
}

func (s *policyModelRepoStub) List(context.Context, uint64, types.ModelType, types.ModelSource) ([]*types.Model, error) {
	result := make([]*types.Model, 0, len(s.models))
	for _, model := range s.models {
		result = append(result, model)
	}
	return result, nil
}

func (s *policyModelRepoStub) Update(_ context.Context, model *types.Model) error {
	s.models[model.ID] = model
	return nil
}

func (s *policyModelRepoStub) Delete(_ context.Context, _ uint64, id string) error {
	delete(s.models, id)
	return nil
}

func (s *policyModelRepoStub) ClearDefaultByType(context.Context, uint, types.ModelType, string) error {
	return nil
}

func policyCatalogJSON(t *testing.T, items ...types.PlatformModelProvider) string {
	t.Helper()
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	return string(raw)
}

func parserProfileJSON(t *testing.T, profile types.ParserProfile) string {
	t.Helper()
	raw, err := json.Marshal(profile)
	require.NoError(t, err)
	return string(raw)
}

func approvedInternalProvider() types.PlatformModelProvider {
	return types.PlatformModelProvider{
		ID:          "example-internal",
		DisplayName: "Example Internal API",
		Adapter:     "generic",
		BaseURL:     "https://llm.example.com/v1",
		Approved:    true,
		ModelTypes:  []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding},
	}
}

func policyContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
}

func TestModelPolicyResolvesApprovedProviderAliasAndLocksBaseURL(t *testing.T) {
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:         "enforce",
		settingRequireExplicitProvider: true,
		settingProviderCatalog:         policyCatalogJSON(t, approvedInternalProvider()),
	})
	svc := NewModelPolicyService(&policyModelRepoStub{models: map[string]*types.Model{}}, settings)
	model := &types.Model{
		ID:     "chat-1",
		Type:   types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "example-internal",
		},
	}

	require.NoError(t, svc.ValidateModelForWrite(policyContext(), model))
	assert.Equal(t, "example-internal", model.Parameters.Provider)
	assert.Equal(t, "https://llm.example.com/v1", model.Parameters.BaseURL)

	prepared, err := svc.PrepareModelForRuntime(policyContext(), model)
	require.NoError(t, err)
	assert.Equal(t, "generic", prepared.Parameters.Provider)
	assert.Equal(t, "https://llm.example.com/v1", prepared.Parameters.BaseURL)
	assert.Equal(t, "example-internal", model.Parameters.Provider, "stored provider id must remain the platform alias")
}

func TestModelPolicyRejectsUnapprovedProviderAndArbitraryGenericURL(t *testing.T) {
	item := approvedInternalProvider()
	item.Approved = false
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:         "enforce",
		settingRequireExplicitProvider: true,
		settingProviderCatalog:         policyCatalogJSON(t, item),
	})
	svc := NewModelPolicyService(&policyModelRepoStub{models: map[string]*types.Model{}}, settings)
	model := &types.Model{
		Type:   types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "example-internal",
			BaseURL:  "https://outside.example/v1",
		},
	}

	err := svc.ValidateModelForWrite(policyContext(), model)
	require.Error(t, err)
	assert.ErrorContains(t, err, "not approved")

	item.Approved = true
	settings.values[settingProviderCatalog] = policyCatalogJSON(t, item)
	err = svc.ValidateModelForWrite(policyContext(), model)
	require.Error(t, err)
	assert.ErrorContains(t, err, "base URL is locked")
}

func TestKnowledgeBasePolicyFixesIngestModelsAndRejectsOverride(t *testing.T) {
	providerItem := approvedInternalProvider()
	models := map[string]*types.Model{
		"embed-fixed": {
			ID: "embed-fixed", TenantID: types.DefaultBuiltinModelTenantID, IsBuiltin: true,
			Type: types.ModelTypeEmbedding, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
			Parameters: types.ModelParameters{Provider: providerItem.ID, BaseURL: providerItem.BaseURL},
		},
		"summary-fixed": {
			ID: "summary-fixed", TenantID: types.DefaultBuiltinModelTenantID, IsBuiltin: true,
			Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
			Parameters: types.ModelParameters{Provider: providerItem.ID, BaseURL: providerItem.BaseURL},
		},
	}
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:        "enforce",
		settingProviderCatalog:        policyCatalogJSON(t, providerItem),
		settingFixedIngestEmbeddingID: "embed-fixed",
		settingFixedIngestSummaryID:   "summary-fixed",
	})
	svc := NewModelPolicyService(&policyModelRepoStub{models: models}, settings)

	kb := &types.KnowledgeBase{EmbeddingModelID: "other", SummaryModelID: "summary-fixed"}
	err := svc.ApplyKnowledgeBasePolicy(policyContext(), kb)
	require.Error(t, err)
	assert.ErrorContains(t, err, "embedding_model_id is fixed")

	kb.EmbeddingModelID = ""
	require.NoError(t, svc.ApplyKnowledgeBasePolicy(policyContext(), kb))
	assert.Equal(t, "embed-fixed", kb.EmbeddingModelID)
	assert.Equal(t, "summary-fixed", kb.SummaryModelID)
}

func TestParserPolicyRejectsUploadOverridesAndLocksParserEngine(t *testing.T) {
	profile := types.ParserProfile{
		ID:                 "sjtu-mineru-v1",
		Engine:             "mineru",
		FileTypes:          []string{"pdf", "pptx", "docx"},
		LockedOverrideKeys: []string{"mineru_endpoint", "mineru_api_key", "mineru_model"},
	}
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:    "enforce",
		settingProviderCatalog:    "[]",
		settingFixedParserProfile: parserProfileJSON(t, profile),
	})
	svc := NewModelPolicyService(&policyModelRepoStub{models: map[string]*types.Model{}}, settings)
	kb := &types.KnowledgeBase{}
	overrides := &types.KnowledgeProcessOverrides{
		ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}},
	}

	err := svc.ValidateProcessOverrides(policyContext(), kb, overrides, []string{"pdf"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "fixed to mineru")

	overrides.ParserEngineRules = nil
	overrides.ParserEngineOverrides = map[string]string{"mineru_endpoint": "https://outside.example"}
	err = svc.ValidateProcessOverrides(policyContext(), kb, overrides, []string{"pdf"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "is locked")

	eff := &types.EffectiveProcessConfig{}
	require.NoError(t, svc.ApplyEffectiveProcessPolicy(policyContext(), eff))
	assert.Equal(t, "mineru", eff.ChunkingConfig.ResolveParserEngine("pdf"))
}

func TestFixedIngestChatModelDoesNotRestrictInteractiveChat(t *testing.T) {
	item := approvedInternalProvider()
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:      "enforce",
		settingProviderCatalog:      policyCatalogJSON(t, item),
		settingFixedIngestSummaryID: "summary-fixed",
	})
	svc := NewModelPolicyService(&policyModelRepoStub{models: map[string]*types.Model{}}, settings)
	chatModel := &types.Model{
		ID: "chat-choice", Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{Provider: item.ID, BaseURL: item.BaseURL},
	}

	_, err := svc.PrepareModelForRuntime(policyContext(), chatModel)
	require.NoError(t, err, "interactive chat may choose any compliant model")

	backgroundCtx := types.WithBackgroundTask(policyContext())
	_, err = svc.PrepareModelForRuntime(backgroundCtx, chatModel)
	require.Error(t, err)
	assert.ErrorContains(t, err, "fixed to summary-fixed")
}

func TestPrepareModelForRuntimeOverridesStoredModelWithFixedBinding(t *testing.T) {
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:        "enforce",
		settingProviderCatalog:        `[]`,
		settingFixedIngestEmbeddingID: "builtin-embedding",
	})
	fixed := &types.Model{ID: "builtin-embedding", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive, IsBuiltin: true, Source: types.ModelSourceLocal}
	svc := NewModelPolicyService(
		&policyModelRepoStub{models: map[string]*types.Model{"builtin-embedding": fixed}},
		settings,
	)

	// Background task requests a pre-policy stored model; enforce mode must
	// converge to the fixed binding instead of failing ingestion.
	stored := &types.Model{ID: "legacy-embedding", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive, Source: types.ModelSourceLocal}
	prepared, err := svc.PrepareModelForRuntime(types.WithBackgroundTask(policyContext()), stored)
	require.NoError(t, err)
	assert.Equal(t, "builtin-embedding", prepared.ID)
}

func TestPrepareModelForRuntimeRejectsWhenFixedModelUnavailable(t *testing.T) {
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:        "enforce",
		settingProviderCatalog:        `[]`,
		settingFixedIngestEmbeddingID: "builtin-missing",
	})
	svc := NewModelPolicyService(
		&policyModelRepoStub{models: map[string]*types.Model{}},
		settings,
	)

	stored := &types.Model{ID: "legacy-embedding", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive, Source: types.ModelSourceLocal}
	_, err := svc.PrepareModelForRuntime(types.WithBackgroundTask(policyContext()), stored)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fixed to builtin-missing")
}

func TestPrepareModelForRuntimeDoesNotInterveneOutsideEnforce(t *testing.T) {
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:        "audit",
		settingProviderCatalog:        `[]`,
		settingFixedIngestEmbeddingID: "builtin-embedding",
	})
	fixed := &types.Model{ID: "builtin-embedding", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive, IsBuiltin: true, Source: types.ModelSourceLocal}
	svc := NewModelPolicyService(
		&policyModelRepoStub{models: map[string]*types.Model{"builtin-embedding": fixed}},
		settings,
	)

	// Audit mode records but never overrides: the stored model stays in use.
	stored := &types.Model{ID: "legacy-embedding", Type: types.ModelTypeEmbedding, Status: types.ModelStatusActive, Source: types.ModelSourceLocal}
	prepared, err := svc.PrepareModelForRuntime(types.WithBackgroundTask(policyContext()), stored)
	require.NoError(t, err)
	assert.Equal(t, "legacy-embedding", prepared.ID)
}

func TestValidateProcessOverridesRequiresFixedVLMWhenEnabledAndEmpty(t *testing.T) {
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode:  "enforce",
		settingProviderCatalog:  `[]`,
		settingFixedIngestVLMID: "builtin-vlm",
	})
	svc := NewModelPolicyService(
		&policyModelRepoStub{models: map[string]*types.Model{}},
		settings,
	)

	overrides := &types.KnowledgeProcessOverrides{
		VLMConfig: &types.VLMConfig{Enabled: true, ModelID: ""},
	}
	err := svc.ValidateProcessOverrides(policyContext(), &types.KnowledgeBase{}, overrides, []string{"pdf"})
	// Previously the empty ModelID skipped validation entirely. Enforce mode
	// now resolves the enabled-but-empty binding to the fixed model and
	// verifies its availability.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "references an unavailable model")
}

func TestValidateProcessOverridesAllowsEnabledEmptyWithoutFixedBinding(t *testing.T) {
	settings := newPolicySettingsStub(map[string]any{
		settingModelPolicyMode: "enforce",
		settingProviderCatalog: `[]`,
	})
	svc := NewModelPolicyService(
		&policyModelRepoStub{models: map[string]*types.Model{}},
		settings,
	)

	overrides := &types.KnowledgeProcessOverrides{
		VLMConfig: &types.VLMConfig{Enabled: true, ModelID: ""},
	}
	err := svc.ValidateProcessOverrides(policyContext(), &types.KnowledgeBase{}, overrides, []string{"pdf"})
	require.NoError(t, err)
}
