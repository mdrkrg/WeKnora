package types

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// WorkspaceProvisioningConfigEnv overrides the default optional config path.
	WorkspaceProvisioningConfigEnv = "WEKNORA_WORKSPACE_PROVISIONING_CONFIG"
	workspaceProvisioningFileName  = "workspace_provisioning.yaml"
	workspaceProvisioningVersion   = 1
)

// WorkspaceProvisioningManagedBy marks provider entries reconciled from the
// deployment workspace provisioning manifest. It is deliberately distinct
// from the builtin_models.yaml owner.
const WorkspaceProvisioningManagedBy = "workspace_provisioning"

// WorkspaceProvisioningConfig declares the deployment-owned provider catalog,
// the governance policy, and overridable defaults applied only to newly
// created tenant resources. Builtin models themselves are declared in
// config/builtin_models.yaml; this manifest only references model IDs.
type WorkspaceProvisioningConfig struct {
	Version   int                           `yaml:"version"`
	Enabled   bool                          `yaml:"enabled"`
	Providers []PlatformModelProvider       `yaml:"providers"`
	Defaults  WorkspaceProvisioningDefaults `yaml:"defaults"`
	// Policy is the deployment-owned governance document. When present while
	// provisioning is enabled it becomes the effective policy (GetPolicy
	// prefers it over the settings KV); fixed ingestion bindings and the
	// locked parser profile then come from the manifest.
	Policy *ModelGovernancePolicy `yaml:"policy,omitempty"`
}

// WorkspaceProvisioningDefaults contains stable IDs rather than credentials.
// Model rows and parser secrets remain deployment-owned shared resources.
type WorkspaceProvisioningDefaults struct {
	ChatModelID      string         `yaml:"chat_model_id"`
	EmbeddingModelID string         `yaml:"embedding_model_id"`
	RerankModelID    string         `yaml:"rerank_model_id"`
	ParserProfile    *ParserProfile `yaml:"parser_profile"`
}

var workspaceProvisioningEnvPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// LoadWorkspaceProvisioningConfig reads the optional deployment manifest. A
// missing file disables the feature for backward compatibility. Other path
// errors fail closed so a bad deployment mount cannot silently disable it.
// Once enabled, parsing and validation are strict so a typo cannot create an
// apparently healthy but unusable workspace.
func LoadWorkspaceProvisioningConfig(configDir string) (*WorkspaceProvisioningConfig, error) {
	path := strings.TrimSpace(os.Getenv(WorkspaceProvisioningConfigEnv))
	if path == "" {
		path = filepath.Join(configDir, workspaceProvisioningFileName)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return &WorkspaceProvisioningConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect workspace provisioning config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace provisioning config is not a regular file")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workspace provisioning config: %w", err)
	}
	cfg, err := decodeWorkspaceProvisioningYAML(string(raw))
	if err != nil {
		return nil, err
	}
	if cfg.Version != workspaceProvisioningVersion {
		return nil, fmt.Errorf("unsupported workspace provisioning version %d", cfg.Version)
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	expanded, err := expandWorkspaceProvisioningEnv(string(raw))
	if err != nil {
		return nil, err
	}
	cfg, err = decodeWorkspaceProvisioningYAML(expanded)
	if err != nil {
		return nil, err
	}
	if err := cfg.normalizeAndValidate(); err != nil {
		return nil, fmt.Errorf("invalid workspace provisioning config: %w", err)
	}
	return cfg, nil
}

func decodeWorkspaceProvisioningYAML(raw string) (*WorkspaceProvisioningConfig, error) {
	decoder := yaml.NewDecoder(bytes.NewBufferString(raw))
	decoder.KnownFields(true)
	var cfg WorkspaceProvisioningConfig
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse workspace provisioning config: %w", err)
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if err == nil {
		return nil, fmt.Errorf("parse workspace provisioning config: multiple YAML documents are not allowed")
	}
	if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse workspace provisioning config: %w", err)
	}
	return &cfg, nil
}

func expandWorkspaceProvisioningEnv(raw string) (string, error) {
	var expansionErr error
	expanded := workspaceProvisioningEnvPattern.ReplaceAllStringFunc(raw, func(placeholder string) string {
		if expansionErr != nil {
			return ""
		}
		match := workspaceProvisioningEnvPattern.FindStringSubmatch(placeholder)
		name := match[1]
		value, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(value) == "" {
			expansionErr = fmt.Errorf("environment variable %s is unset or empty", name)
			return ""
		}
		return value
	})
	if expansionErr != nil {
		return "", expansionErr
	}
	return expanded, nil
}

func (cfg *WorkspaceProvisioningConfig) normalizeAndValidate() error {
	providerIDs := make(map[string]struct{}, len(cfg.Providers))
	for i := range cfg.Providers {
		provider := &cfg.Providers[i]
		provider.ID = strings.ToLower(strings.TrimSpace(provider.ID))
		provider.Adapter = strings.ToLower(strings.TrimSpace(provider.Adapter))
		provider.BaseURL = strings.TrimSpace(provider.BaseURL)
		if provider.ID == "" || provider.Adapter == "" {
			return fmt.Errorf("provider %d requires id and adapter", i)
		}
		if _, duplicate := providerIDs[provider.ID]; duplicate {
			return fmt.Errorf("duplicate provider id %q", provider.ID)
		}
		providerIDs[provider.ID] = struct{}{}
	}

	if cfg.Policy != nil {
		switch cfg.Policy.Mode {
		case ModelPolicyModeOff, ModelPolicyModeAudit, ModelPolicyModeEnforce:
		default:
			return fmt.Errorf("policy mode must be off, audit, or enforce")
		}
		if cfg.Policy.ParserProfile != nil {
			if err := normalizeWorkspaceParserProfile(cfg.Policy.ParserProfile); err != nil {
				return fmt.Errorf("invalid policy parser profile: %w", err)
			}
		}
	}

	cfg.Defaults.ChatModelID = strings.TrimSpace(cfg.Defaults.ChatModelID)
	cfg.Defaults.EmbeddingModelID = strings.TrimSpace(cfg.Defaults.EmbeddingModelID)
	cfg.Defaults.RerankModelID = strings.TrimSpace(cfg.Defaults.RerankModelID)
	for _, purpose := range []struct {
		name string
		id   string
	}{{"chat", cfg.Defaults.ChatModelID}, {"embedding", cfg.Defaults.EmbeddingModelID}, {"rerank", cfg.Defaults.RerankModelID}} {
		if purpose.id == "" {
			return fmt.Errorf("default %s model id is required", purpose.name)
		}
	}
	if cfg.Defaults.ParserProfile == nil {
		return fmt.Errorf("default parser profile is required")
	}
	if len(cfg.Defaults.ParserProfile.Overrides) > 0 {
		return fmt.Errorf("parser overrides belong in policy.parser_profile, not defaults.parser_profile")
	}
	return normalizeWorkspaceParserProfile(cfg.Defaults.ParserProfile)
}

func normalizeWorkspaceParserProfile(profile *ParserProfile) error {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Engine = strings.ToLower(strings.TrimSpace(profile.Engine))
	if profile.ID == "" {
		return fmt.Errorf("parser profile id is required")
	}
	if profile.Engine == "" {
		return fmt.Errorf("parser engine is required")
	}
	seen := make(map[string]struct{}, len(profile.FileTypes))
	fileTypes := make([]string, 0, len(profile.FileTypes))
	for _, value := range profile.FileTypes {
		fileType := normalizeWorkspaceFileType(value)
		if fileType == "" {
			continue
		}
		if _, duplicate := seen[fileType]; duplicate {
			continue
		}
		seen[fileType] = struct{}{}
		fileTypes = append(fileTypes, fileType)
	}
	if len(fileTypes) == 0 {
		return fmt.Errorf("parser profile file types are required")
	}
	profile.FileTypes = fileTypes

	if len(profile.Overrides) > 0 {
		normalizedOverrides := make(map[string]string, len(profile.Overrides))
		for key, value := range profile.Overrides {
			key = normalizeWorkspaceFileType(key)
			if key == "" {
				continue
			}
			normalizedOverrides[key] = strings.TrimSpace(value)
		}
		profile.Overrides = normalizedOverrides
	}

	return nil
}

func normalizeWorkspaceFileType(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}

// ApplyTenantDefaults mutates only empty creation-time fields.
func (cfg *WorkspaceProvisioningConfig) ApplyTenantDefaults(tenant *Tenant) {
	if cfg == nil || !cfg.Enabled || tenant == nil {
		return
	}
	if tenant.RetrievalConfig == nil {
		tenant.RetrievalConfig = &RetrievalConfig{}
	}
	if strings.TrimSpace(tenant.RetrievalConfig.RerankModelID) == "" {
		tenant.RetrievalConfig.RerankModelID = cfg.Defaults.RerankModelID
	}
}

// ApplyKnowledgeBaseModelDefaults fills empty creation-time model fields on a
// knowledge base. It runs on every KB save path (create and update) — empty
// fields are benign to fill, and fixed governance bindings override afterwards.
func (cfg *WorkspaceProvisioningConfig) ApplyKnowledgeBaseModelDefaults(kb *KnowledgeBase) {
	if cfg == nil || !cfg.Enabled || kb == nil {
		return
	}
	if strings.TrimSpace(kb.SummaryModelID) == "" {
		kb.SummaryModelID = cfg.Defaults.ChatModelID
	}
	if kb.NeedsEmbeddingModel() && strings.TrimSpace(kb.EmbeddingModelID) == "" {
		kb.EmbeddingModelID = cfg.Defaults.EmbeddingModelID
	}
}

// ApplyKnowledgeBaseParserDefaults appends the deployment default parser rule
// for file types the KB does not yet cover. It must run only on creation
// paths: re-applying it on every update would silently converge pre-existing
// KBs to the manifest default parser. Copy/duplicate inherit the source KB's
// rules and deliberately skip this.
func (cfg *WorkspaceProvisioningConfig) ApplyKnowledgeBaseParserDefaults(kb *KnowledgeBase) {
	if cfg == nil || !cfg.Enabled || kb == nil {
		return
	}
	profile := cfg.Defaults.ParserProfile
	if profile == nil {
		return
	}
	covered := make(map[string]struct{})
	for _, rule := range kb.ChunkingConfig.ParserEngineRules {
		for _, fileType := range rule.FileTypes {
			covered[normalizeWorkspaceFileType(fileType)] = struct{}{}
		}
	}
	missing := make([]string, 0, len(profile.FileTypes))
	for _, fileType := range profile.FileTypes {
		if _, explicit := covered[normalizeWorkspaceFileType(fileType)]; !explicit {
			missing = append(missing, normalizeWorkspaceFileType(fileType))
		}
	}
	if len(missing) > 0 {
		kb.ChunkingConfig.ParserEngineRules = append(kb.ChunkingConfig.ParserEngineRules, ParserEngineRule{
			FileTypes: missing,
			Engine:    profile.Engine,
		})
	}
}

// ApplyAgentDefaults injects chat/rerank IDs only when the user did not make
// an explicit selection. Agents with knowledge retrieval disabled do not need
// a reranker.
func (cfg *WorkspaceProvisioningConfig) ApplyAgentDefaults(agent *CustomAgent, tenant *Tenant) {
	if cfg == nil || !cfg.Enabled || agent == nil {
		return
	}
	if strings.TrimSpace(agent.Config.ModelID) == "" {
		agent.Config.ModelID = cfg.Defaults.ChatModelID
	}
	if strings.TrimSpace(agent.Config.RerankModelID) != "" || !agentUsesKnowledgeSearch(agent) {
		return
	}
	if tenant != nil && tenant.RetrievalConfig != nil && strings.TrimSpace(tenant.RetrievalConfig.RerankModelID) != "" {
		agent.Config.RerankModelID = tenant.RetrievalConfig.RerankModelID
		return
	}
	agent.Config.RerankModelID = cfg.Defaults.RerankModelID
}

func agentUsesKnowledgeSearch(agent *CustomAgent) bool {
	if agent == nil || agent.Config.KBSelectionMode == "none" {
		return false
	}
	if len(agent.Config.AllowedTools) == 0 {
		return true
	}
	for _, tool := range agent.Config.AllowedTools {
		if tool == "knowledge_search" {
			return true
		}
	}
	return false
}

// ApplyModelDefaults computes tenant-aware response flags without persisting
// per-tenant copies of shared builtin models.
func (cfg *WorkspaceProvisioningConfig) ApplyModelDefaults(models []*Model, tenantID uint64) {
	if cfg == nil || !cfg.Enabled {
		return
	}
	defaults := map[ModelType]string{
		ModelTypeKnowledgeQA: cfg.Defaults.ChatModelID,
		ModelTypeEmbedding:   cfg.Defaults.EmbeddingModelID,
		ModelTypeRerank:      cfg.Defaults.RerankModelID,
	}
	for modelType, defaultID := range defaults {
		hasTenantDefault := false
		for _, model := range models {
			if model != nil && model.Type == modelType && model.Status != ModelStatusActive {
				model.IsDefault = false
			}
			if model != nil && model.TenantID == tenantID && model.Type == modelType &&
				model.Status == ModelStatusActive && model.IsDefault {
				hasTenantDefault = true
				break
			}
		}
		for _, model := range models {
			if model == nil || model.Type != modelType || !model.IsBuiltin {
				continue
			}
			model.IsDefault = !hasTenantDefault && model.ID == defaultID && model.Status == ModelStatusActive
		}
	}
}
