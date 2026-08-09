package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	settingModelPolicyMode         = "model.policy_mode"
	settingRequireExplicitProvider = "model.require_explicit_provider"
	settingProviderCatalog         = "model.provider_catalog"
	settingFixedIngestEmbeddingID  = "model.fixed_ingest_embedding_id"
	settingFixedIngestSummaryID    = "model.fixed_ingest_summary_id"
	settingFixedIngestVLMID        = "model.fixed_ingest_vlm_id"
	settingFixedIngestASRID        = "model.fixed_ingest_asr_id"
	settingFixedParserProfile      = "parser.fixed_profile"
)

var platformProviderIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type modelPolicyService struct {
	repo                  interfaces.ModelRepository
	settings              interfaces.SystemSettingService
	workspaceProvisioning *types.WorkspaceProvisioningConfig
}

func NewModelPolicyService(
	repo interfaces.ModelRepository,
	settings interfaces.SystemSettingService,
) interfaces.ModelPolicyService {
	return &modelPolicyService{repo: repo, settings: settings}
}

// NewModelPolicyServiceWithWorkspaceProvisioning wires deployment defaults
// into the existing governance service. The ordinary constructor remains for
// compatibility and behaves exactly as before when provisioning is disabled.
func NewModelPolicyServiceWithWorkspaceProvisioning(
	repo interfaces.ModelRepository,
	settings interfaces.SystemSettingService,
	workspaceProvisioning *types.WorkspaceProvisioningConfig,
) interfaces.ModelPolicyService {
	return &modelPolicyService{
		repo:                  repo,
		settings:              settings,
		workspaceProvisioning: workspaceProvisioning,
	}
}

func (s *modelPolicyService) GetPolicy(ctx context.Context) (*types.ModelGovernancePolicy, error) {
	// The deployment manifest is the authoritative policy source while
	// workspace provisioning is enabled; the settings KV is a fallback for
	// deployments without a manifest (and for unit tests).
	if s != nil && s.workspaceProvisioning != nil && s.workspaceProvisioning.Enabled &&
		s.workspaceProvisioning.Policy != nil {
		return s.workspaceProvisioning.Policy, nil
	}
	if s == nil || s.settings == nil {
		return &types.ModelGovernancePolicy{Mode: types.ModelPolicyModeOff}, nil
	}

	mode := types.ModelPolicyMode(strings.TrimSpace(s.settings.GetString(
		ctx, settingModelPolicyMode, "", string(types.ModelPolicyModeOff),
	)))
	if !isValidPolicyMode(mode) {
		return nil, fmt.Errorf("invalid model policy mode %q", mode)
	}

	policy := &types.ModelGovernancePolicy{
		Mode:                    mode,
		RequireExplicitProvider: s.settings.GetBool(ctx, settingRequireExplicitProvider, "", true),
		FixedIngestEmbeddingID:  strings.TrimSpace(s.settings.GetString(ctx, settingFixedIngestEmbeddingID, "", "")),
		FixedIngestSummaryID:    strings.TrimSpace(s.settings.GetString(ctx, settingFixedIngestSummaryID, "", "")),
		FixedIngestVLMID:        strings.TrimSpace(s.settings.GetString(ctx, settingFixedIngestVLMID, "", "")),
		FixedIngestASRID:        strings.TrimSpace(s.settings.GetString(ctx, settingFixedIngestASRID, "", "")),
	}

	rawParser := strings.TrimSpace(s.settings.GetString(ctx, settingFixedParserProfile, "", ""))
	if rawParser != "" {
		var profile types.ParserProfile
		if err := json.Unmarshal([]byte(rawParser), &profile); err != nil {
			return nil, fmt.Errorf("invalid parser fixed profile: %w", err)
		}
		if err := normalizeParserProfile(&profile); err != nil {
			return nil, err
		}
		policy.ParserProfile = &profile
	}
	return policy, nil
}

func (s *modelPolicyService) ValidateModelForWrite(ctx context.Context, model *types.Model) error {
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return s.handleViolation(ctx, types.ModelPolicyModeEnforce, err)
	}
	_, err = s.applyProviderPolicy(ctx, policy, model, true)
	return s.handleViolation(ctx, policy.Mode, err)
}

func (s *modelPolicyService) PrepareModelForRuntime(
	ctx context.Context,
	model *types.Model,
) (*types.Model, error) {
	if model == nil {
		return nil, apperrors.NewBadRequestError("model is required")
	}
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return nil, err
	}
	if types.IsBackgroundTask(ctx) {
		fixedID := fixedModelIDForType(policy, model.Type)
		if fixedID != "" && fixedID != model.ID {
			// Enforce mode: prefer overriding the stored (possibly pre-policy)
			// model with the fixed binding so ingestion converges instead of
			// failing for KBs created before the policy took effect. The
			// override is logged; when the fixed model itself is unavailable,
			// fall back to the violation path so a broken binding is not
			// silently ignored. Off/audit modes never intervene.
			if policy.Mode == types.ModelPolicyModeEnforce {
				if fixed, err := s.repo.GetByID(ctx, 0, fixedID); err == nil && fixed != nil &&
					fixed.Status == types.ModelStatusActive {
					logger.Warnf(ctx, "[model-policy] background %s model %s overridden by fixed %s",
						model.Type, model.ID, fixedID)
					model = fixed
				} else {
					if err != nil {
						logger.Errorf(ctx, "[model-policy] fixed %s model lookup failed: %v", model.Type, err)
					}
					err = fmt.Errorf("background %s model is fixed to %s", model.Type, fixedID)
					if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
						return nil, handled
					}
				}
			} else {
				err = fmt.Errorf("background %s model is fixed to %s", model.Type, fixedID)
				if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
					return nil, handled
				}
			}
		}
	}
	prepared, err := s.applyProviderPolicy(ctx, policy, model, false)
	if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
		return nil, handled
	}
	if prepared == nil {
		prepared = cloneModel(model)
	}
	return prepared, nil
}

func (s *modelPolicyService) FilterModelsForCaller(
	ctx context.Context,
	models []*types.Model,
) []*types.Model {
	if types.IsSystemAdminFromContext(ctx) {
		return models
	}
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		logger.Errorf(ctx, "[model-policy] failed to load policy while filtering models: %v", err)
		return nil
	}
	if policy.Mode != types.ModelPolicyModeEnforce {
		return models
	}
	result := make([]*types.Model, 0, len(models))
	for _, model := range models {
		if _, err := s.applyProviderPolicy(ctx, policy, model, false); err == nil {
			result = append(result, model)
		}
	}
	if tenantID, ok := types.TenantIDFromContext(ctx); ok && s.workspaceProvisioning != nil {
		s.workspaceProvisioning.ApplyModelDefaults(result, tenantID)
	}
	return result
}

func (s *modelPolicyService) ApplyKnowledgeBasePolicy(
	ctx context.Context,
	kb *types.KnowledgeBase,
) error {
	if kb == nil {
		return apperrors.NewBadRequestError("knowledge base is required")
	}
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return err
	}

	// Remember what the caller actually supplied. Deployment defaults are
	// overridable, while explicit values must still conflict with enforce-mode
	// fixed bindings exactly as they did before this feature.
	summaryWasExplicit := strings.TrimSpace(kb.SummaryModelID) != ""
	embeddingWasExplicit := strings.TrimSpace(kb.EmbeddingModelID) != ""
	originalParserRules := cloneParserRules(kb.ChunkingConfig.ParserEngineRules)
	if s.workspaceProvisioning != nil {
		s.workspaceProvisioning.ApplyKnowledgeBaseModelDefaults(kb)
		if types.IsKnowledgeBaseCreationDefaults(ctx) {
			// Default parser rules apply only to creation: re-applying on every
			// save would silently converge pre-existing KBs to the manifest
			// parser. Copy/duplicate inherit the source rules and skip this.
			s.workspaceProvisioning.ApplyKnowledgeBaseParserDefaults(kb)
		}
	}
	if policy.Mode == types.ModelPolicyModeEnforce {
		if !summaryWasExplicit && policy.FixedIngestSummaryID != "" {
			kb.SummaryModelID = policy.FixedIngestSummaryID
		}
		if !embeddingWasExplicit && policy.FixedIngestEmbeddingID != "" {
			kb.EmbeddingModelID = policy.FixedIngestEmbeddingID
		}
	}
	if s.workspaceProvisioning != nil && s.workspaceProvisioning.Enabled {
		if !summaryWasExplicit && !(policy.Mode == types.ModelPolicyModeEnforce && policy.FixedIngestSummaryID != "") {
			if err := s.validateWorkspaceDefaultReference(
				ctx, "chat", kb.SummaryModelID, types.ModelTypeKnowledgeQA,
			); err != nil {
				return err
			}
		}
		if kb.NeedsEmbeddingModel() && !embeddingWasExplicit &&
			!(policy.Mode == types.ModelPolicyModeEnforce && policy.FixedIngestEmbeddingID != "") {
			if err := s.validateWorkspaceDefaultReference(
				ctx, "embedding", kb.EmbeddingModelID, types.ModelTypeEmbedding,
			); err != nil {
				return err
			}
		}
	}

	bindings := []struct {
		field     string
		requested *string
		fixed     string
		expected  types.ModelType
		required  bool
	}{
		{"embedding_model_id", &kb.EmbeddingModelID, policy.FixedIngestEmbeddingID, types.ModelTypeEmbedding, kb.NeedsEmbeddingModel()},
		// The summary model is only required when the platform actually fixed
		// one; without a binding, empty means "upstream default behavior".
		{"summary_model_id", &kb.SummaryModelID, policy.FixedIngestSummaryID, types.ModelTypeKnowledgeQA, policy.FixedIngestSummaryID != ""},
	}
	for _, binding := range bindings {
		if err := s.applyModelBinding(ctx, policy, binding.field, binding.requested, binding.fixed, binding.expected, binding.required); err != nil {
			return err
		}
	}
	if kb.VLMConfig.Enabled || kb.VLMConfig.ModelID != "" {
		if err := s.applyModelBinding(ctx, policy, "vlm_config.model_id", &kb.VLMConfig.ModelID,
			policy.FixedIngestVLMID, types.ModelTypeVLLM, kb.VLMConfig.Enabled); err != nil {
			return err
		}
	}
	if kb.ASRConfig.Enabled || kb.ASRConfig.ModelID != "" {
		if err := s.applyModelBinding(ctx, policy, "asr_config.model_id", &kb.ASRConfig.ModelID,
			policy.FixedIngestASRID, types.ModelTypeASR, kb.ASRConfig.Enabled); err != nil {
			return err
		}
	}

	if policy.ParserProfile != nil {
		// Validate only the caller-supplied rules. Defaults may intentionally be
		// superseded by a higher-priority fixed profile and must not be reported
		// as a user conflict.
		if err := validateParserRules(originalParserRules, policy.ParserProfile); err != nil {
			if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
				return handled
			}
		}
		if policy.Mode == types.ModelPolicyModeEnforce {
			kb.ChunkingConfig.ParserEngineRules = applyFixedParserRules(
				kb.ChunkingConfig.ParserEngineRules, policy.ParserProfile,
			)
		}
	}
	return nil
}

// validateWorkspaceDefaultReference checks a KB model reference filled from
// manifest defaults; see validateWorkspaceDefaultModel.
func (s *modelPolicyService) validateWorkspaceDefaultReference(
	ctx context.Context,
	purpose string,
	id string,
	expected types.ModelType,
) error {
	return validateWorkspaceDefaultModel(
		ctx, s.repo, s, 0, purpose, id, expected, true, true,
	)
}

func (s *modelPolicyService) ValidateProcessOverrides(
	ctx context.Context,
	kb *types.KnowledgeBase,
	overrides *types.KnowledgeProcessOverrides,
	fileTypes []string,
) error {
	if overrides == nil {
		return nil
	}
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return err
	}

	if overrides.VLMConfig != nil && overrides.VLMConfig.ModelID != "" {
		requested := overrides.VLMConfig.ModelID
		if err := s.applyModelBinding(ctx, policy, "process_config.vlm_config.model_id", &requested,
			policy.FixedIngestVLMID, types.ModelTypeVLLM, overrides.VLMConfig.Enabled); err != nil {
			return err
		}
	}
	if overrides.ASRConfig != nil && overrides.ASRConfig.ModelID != "" {
		requested := overrides.ASRConfig.ModelID
		if err := s.applyModelBinding(ctx, policy, "process_config.asr_config.model_id", &requested,
			policy.FixedIngestASRID, types.ModelTypeASR, overrides.ASRConfig.Enabled); err != nil {
			return err
		}
	}

	profile := policy.ParserProfile
	if profile == nil || !profileAppliesToAny(profile, fileTypes) {
		return nil
	}
	rules := overrides.ParserEngineRules
	if overrides.ChunkingConfig != nil && len(overrides.ChunkingConfig.ParserEngineRules) > 0 {
		rules = overrides.ChunkingConfig.ParserEngineRules
	}
	if err := validateParserRules(rules, profile); err != nil {
		if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
			return handled
		}
	}
	locked := stringSet(profile.LockedOverrideKeys)
	for key := range overrides.ParserEngineOverrides {
		if _, ok := locked[key]; ok {
			err := fmt.Errorf("parser override %q is locked by platform profile %s", key, profile.ID)
			if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
				return handled
			}
		}
	}
	return nil
}

func (s *modelPolicyService) ApplyEffectiveProcessPolicy(
	ctx context.Context,
	eff *types.EffectiveProcessConfig,
) error {
	if eff == nil {
		return nil
	}
	policy, err := s.GetPolicy(ctx)
	if err != nil {
		return err
	}
	if policy.Mode != types.ModelPolicyModeEnforce {
		return nil
	}
	if eff.VLMConfig.Enabled && policy.FixedIngestVLMID != "" {
		eff.VLMConfig.ModelID = policy.FixedIngestVLMID
	}
	if eff.ASRConfig.Enabled && policy.FixedIngestASRID != "" {
		eff.ASRConfig.ModelID = policy.FixedIngestASRID
	}
	if policy.ParserProfile != nil {
		eff.ChunkingConfig.ParserEngineRules = applyFixedParserRules(
			eff.ChunkingConfig.ParserEngineRules, policy.ParserProfile,
		)
	}
	return nil
}

func (s *modelPolicyService) applyModelBinding(
	ctx context.Context,
	policy *types.ModelGovernancePolicy,
	field string,
	requested *string,
	fixedID string,
	expected types.ModelType,
	required bool,
) error {
	if requested == nil {
		return nil
	}
	*requested = strings.TrimSpace(*requested)
	if fixedID != "" && *requested != "" && *requested != fixedID {
		err := fmt.Errorf("%s is fixed by platform policy to %s", field, fixedID)
		if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
			return handled
		}
	}
	if policy.Mode == types.ModelPolicyModeEnforce && fixedID != "" {
		*requested = fixedID
	}
	if *requested == "" {
		if required && policy.Mode == types.ModelPolicyModeEnforce {
			err := fmt.Errorf("%s is required by platform policy", field)
			if handled := s.handleViolation(ctx, policy.Mode, err); handled != nil {
				return handled
			}
		}
		return nil
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	model, err := s.repo.GetByID(ctx, tenantID, *requested)
	if err != nil {
		return err
	}
	if model == nil || model.Status != types.ModelStatusActive {
		err = fmt.Errorf("%s references an unavailable model", field)
	} else if model.Type != expected {
		err = fmt.Errorf("%s must reference a %s model", field, expected)
	} else {
		_, err = s.applyProviderPolicy(ctx, policy, model, false)
	}
	return s.handleViolation(ctx, policy.Mode, err)
}

func (s *modelPolicyService) applyProviderPolicy(
	ctx context.Context,
	policy *types.ModelGovernancePolicy,
	model *types.Model,
	mutate bool,
) (*types.Model, error) {
	if model == nil {
		return nil, fmt.Errorf("model is required")
	}
	prepared := cloneModel(model)
	if model.Source == types.ModelSourceLocal {
		return prepared, nil
	}

	catalog, err := s.loadProviderCatalog(ctx)
	if err != nil {
		return nil, err
	}
	if policy.Mode == types.ModelPolicyModeOff && len(catalog) == 0 {
		return prepared, nil
	}

	providerID := strings.ToLower(strings.TrimSpace(prepared.Parameters.Provider))
	if providerID == "" && !policy.RequireExplicitProvider {
		providerID = string(provider.DetectProvider(prepared.Parameters.BaseURL))
		if mutate {
			model.Parameters.Provider = providerID
		}
		prepared.Parameters.Provider = providerID
	}
	if providerID == "" {
		return nil, fmt.Errorf("remote model provider must be explicit")
	}

	var profile *types.PlatformModelProvider
	for i := range catalog {
		if catalog[i].ID == providerID {
			profile = &catalog[i]
			break
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("provider %q is not in the platform catalog", providerID)
	}
	if !profile.Approved {
		return nil, fmt.Errorf("provider %q is not approved", providerID)
	}
	if !platformProviderSupportsType(*profile, model.Type) {
		return nil, fmt.Errorf("provider %q does not allow model type %s", providerID, model.Type)
	}

	adapter, ok := provider.Get(provider.ProviderName(profile.Adapter))
	if !ok {
		return nil, fmt.Errorf("provider %q uses unknown adapter %q", providerID, profile.Adapter)
	}
	effectiveBaseURL := strings.TrimSpace(profile.BaseURL)
	if effectiveBaseURL == "" {
		effectiveBaseURL = strings.TrimSpace(adapter.Info().GetDefaultURL(model.Type))
	}
	requestedBaseURL := strings.TrimSpace(prepared.Parameters.BaseURL)
	if !profile.AllowCustomBaseURL {
		if effectiveBaseURL == "" {
			return nil, fmt.Errorf("provider %q has no platform base URL for model type %s", providerID, model.Type)
		}
		if requestedBaseURL != "" && effectiveBaseURL != "" && !sameBaseURL(requestedBaseURL, effectiveBaseURL) {
			return nil, fmt.Errorf("provider %q base URL is locked by platform policy", providerID)
		}
		prepared.Parameters.BaseURL = effectiveBaseURL
		if mutate {
			model.Parameters.BaseURL = effectiveBaseURL
		}
	} else if requestedBaseURL == "" {
		prepared.Parameters.BaseURL = effectiveBaseURL
		if mutate {
			model.Parameters.BaseURL = effectiveBaseURL
		}
	}
	// Stored Provider remains the platform provider ID. Runtime constructors
	// receive the compiled adapter name instead.
	prepared.Parameters.Provider = profile.Adapter
	return prepared, nil
}

func (s *modelPolicyService) loadProviderCatalog(ctx context.Context) ([]types.PlatformModelProvider, error) {
	if s == nil || s.settings == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(s.settings.GetString(ctx, settingProviderCatalog, "", "[]"))
	if raw == "" {
		raw = "[]"
	}
	var items []types.PlatformModelProvider
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("invalid platform provider catalog: %w", err)
	}
	seen := make(map[string]struct{}, len(items))
	for i := range items {
		if err := normalizePlatformProvider(&items[i]); err != nil {
			return nil, err
		}
		if _, exists := seen[items[i].ID]; exists {
			return nil, fmt.Errorf("duplicate platform provider id %q", items[i].ID)
		}
		seen[items[i].ID] = struct{}{}
	}
	return items, nil
}

// replacePlatformProviders writes the complete deployment-owned catalog. It
// intentionally removes runtime-created rows while YAML provisioning is
// enabled so the catalog cannot have two competing configuration sources.
func (s *modelPolicyService) replacePlatformProviders(
	ctx context.Context,
	configured []types.PlatformModelProvider,
) error {
	if s == nil || s.settings == nil {
		return fmt.Errorf("system settings service is unavailable")
	}
	return s.saveProviderCatalog(ctx, configured)
}

func (s *modelPolicyService) saveProviderCatalog(ctx context.Context, items []types.PlatformModelProvider) error {
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = s.settings.Update(ctx, settingProviderCatalog, string(raw))
	return err
}

func (s *modelPolicyService) handleViolation(
	ctx context.Context,
	mode types.ModelPolicyMode,
	err error,
) error {
	if err == nil {
		return nil
	}
	if mode == types.ModelPolicyModeAudit {
		logger.Warnf(ctx, "[model-policy][audit] %v", err)
		return nil
	}
	if mode == types.ModelPolicyModeOff {
		return nil
	}
	if _, ok := apperrors.IsAppError(err); ok {
		return err
	}
	return apperrors.NewBadRequestError(err.Error())
}

func normalizePlatformProvider(item *types.PlatformModelProvider) error {
	item.ID = strings.ToLower(strings.TrimSpace(item.ID))
	item.Adapter = strings.ToLower(strings.TrimSpace(item.Adapter))
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.Description = strings.TrimSpace(item.Description)
	item.BaseURL = strings.TrimSpace(item.BaseURL)
	if !platformProviderIDPattern.MatchString(item.ID) {
		return fmt.Errorf("provider id must match %s", platformProviderIDPattern.String())
	}
	adapter, ok := provider.Get(provider.ProviderName(item.Adapter))
	if !ok {
		return fmt.Errorf("unknown provider adapter %q", item.Adapter)
	}
	if item.DisplayName == "" {
		item.DisplayName = adapter.Info().DisplayName
	}
	if item.Description == "" {
		item.Description = adapter.Info().Description
	}
	if item.BaseURL != "" {
		parsed, err := url.ParseRequestURI(item.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("provider base_url must be an absolute HTTP(S) URL")
		}
	}
	if len(item.ModelTypes) == 0 {
		item.ModelTypes = append([]types.ModelType(nil), adapter.Info().ModelTypes...)
	}
	allowed := make(map[types.ModelType]struct{}, len(adapter.Info().ModelTypes))
	for _, mt := range adapter.Info().ModelTypes {
		allowed[mt] = struct{}{}
	}
	seen := make(map[types.ModelType]struct{}, len(item.ModelTypes))
	normalizedTypes := make([]types.ModelType, 0, len(item.ModelTypes))
	for _, mt := range item.ModelTypes {
		if _, ok := allowed[mt]; !ok {
			return fmt.Errorf("adapter %q does not support model type %s", item.Adapter, mt)
		}
		if _, duplicate := seen[mt]; duplicate {
			continue
		}
		seen[mt] = struct{}{}
		normalizedTypes = append(normalizedTypes, mt)
	}
	item.ModelTypes = normalizedTypes
	return nil
}

func normalizeParserProfile(profile *types.ParserProfile) error {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Engine = strings.ToLower(strings.TrimSpace(profile.Engine))
	if profile.ID == "" || profile.Engine == "" {
		return fmt.Errorf("parser profile id and engine are required")
	}
	profile.FileTypes = normalizedFileTypes(profile.FileTypes)
	if len(profile.FileTypes) == 0 {
		return fmt.Errorf("parser profile must lock at least one file type")
	}
	locked := make(map[string]struct{}, len(profile.LockedOverrideKeys))
	normalizedLocked := make([]string, 0, len(profile.LockedOverrideKeys))
	for _, key := range profile.LockedOverrideKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, duplicate := locked[key]; duplicate {
			continue
		}
		locked[key] = struct{}{}
		normalizedLocked = append(normalizedLocked, key)
	}
	profile.LockedOverrideKeys = normalizedLocked
	return nil
}

func validateParserRules(rules []types.ParserEngineRule, profile *types.ParserProfile) error {
	if profile == nil {
		return nil
	}
	locked := stringSet(profile.FileTypes)
	for _, rule := range rules {
		for _, fileType := range rule.FileTypes {
			if _, ok := locked[normalizeFileType(fileType)]; ok && strings.TrimSpace(rule.Engine) != profile.Engine {
				return fmt.Errorf("parser engine for %s is fixed to %s by profile %s", fileType, profile.Engine, profile.ID)
			}
		}
	}
	return nil
}

func applyFixedParserRules(rules []types.ParserEngineRule, profile *types.ParserProfile) []types.ParserEngineRule {
	if profile == nil {
		return rules
	}
	locked := stringSet(profile.FileTypes)
	result := make([]types.ParserEngineRule, 0, len(rules)+1)
	for _, rule := range rules {
		kept := make([]string, 0, len(rule.FileTypes))
		for _, fileType := range rule.FileTypes {
			if _, ok := locked[normalizeFileType(fileType)]; !ok {
				kept = append(kept, fileType)
			}
		}
		if len(kept) > 0 {
			rule.FileTypes = kept
			result = append(result, rule)
		}
	}
	result = append(result, types.ParserEngineRule{
		FileTypes: append([]string(nil), profile.FileTypes...),
		Engine:    profile.Engine,
	})
	return result
}

func cloneParserRules(rules []types.ParserEngineRule) []types.ParserEngineRule {
	if rules == nil {
		return nil
	}
	result := make([]types.ParserEngineRule, len(rules))
	for i := range rules {
		result[i] = rules[i]
		result[i].FileTypes = append([]string(nil), rules[i].FileTypes...)
	}
	return result
}

func fixedModelIDForType(policy *types.ModelGovernancePolicy, modelType types.ModelType) string {
	if policy == nil {
		return ""
	}
	switch modelType {
	case types.ModelTypeEmbedding:
		return policy.FixedIngestEmbeddingID
	case types.ModelTypeKnowledgeQA:
		return policy.FixedIngestSummaryID
	case types.ModelTypeVLLM:
		return policy.FixedIngestVLMID
	case types.ModelTypeASR:
		return policy.FixedIngestASRID
	default:
		return ""
	}
}

func platformProviderSupportsType(item types.PlatformModelProvider, modelType types.ModelType) bool {
	if modelType == "" {
		return true
	}
	for _, mt := range item.ModelTypes {
		if mt == modelType {
			return true
		}
	}
	return false
}

func profileAppliesToAny(profile *types.ParserProfile, fileTypes []string) bool {
	if len(fileTypes) == 0 {
		return true
	}
	for _, fileType := range fileTypes {
		if profileAppliesTo(profile, fileType) {
			return true
		}
	}
	return false
}

func profileAppliesTo(profile *types.ParserProfile, fileType string) bool {
	if profile == nil {
		return false
	}
	wanted := normalizeFileType(fileType)
	for _, candidate := range profile.FileTypes {
		if normalizeFileType(candidate) == wanted {
			return true
		}
	}
	return false
}

func normalizedFileTypes(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, fileType := range in {
		fileType = normalizeFileType(fileType)
		if fileType == "" {
			continue
		}
		if _, duplicate := seen[fileType]; duplicate {
			continue
		}
		seen[fileType] = struct{}{}
		out = append(out, fileType)
	}
	sort.Strings(out)
	return out
}

func normalizeFileType(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[normalizeFileType(value)] = struct{}{}
	}
	return result
}

func sameBaseURL(left, right string) bool {
	return strings.TrimRight(strings.TrimSpace(left), "/") == strings.TrimRight(strings.TrimSpace(right), "/")
}

func cloneModel(model *types.Model) *types.Model {
	if model == nil {
		return nil
	}
	cloned := *model
	cloned.Parameters = model.Parameters
	cloned.Parameters.ExtraConfig = cloneStringMap(model.Parameters.ExtraConfig)
	cloned.Parameters.CustomHeaders = cloneStringMap(model.Parameters.CustomHeaders)
	return &cloned
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func isValidPolicyMode(mode types.ModelPolicyMode) bool {
	return mode == types.ModelPolicyModeOff || mode == types.ModelPolicyModeAudit || mode == types.ModelPolicyModeEnforce
}
