package types

// ModelPolicyMode controls how platform model and parser policy violations are handled.
type ModelPolicyMode string

const (
	ModelPolicyModeOff     ModelPolicyMode = "off"
	ModelPolicyModeAudit   ModelPolicyMode = "audit"
	ModelPolicyModeEnforce ModelPolicyMode = "enforce"
)

// PlatformModelProvider is a deployment-approved connection definition backed
// by one compiled provider adapter. BaseURL is platform-owned unless
// AllowCustomBaseURL is explicitly enabled.
type PlatformModelProvider struct {
	ID                 string      `yaml:"id" json:"id"`
	DisplayName        string      `yaml:"display_name" json:"display_name"`
	Description        string      `yaml:"description,omitempty" json:"description,omitempty"`
	Adapter            string      `yaml:"adapter" json:"adapter"`
	BaseURL            string      `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	Approved           bool        `yaml:"approved" json:"approved"`
	AllowCustomBaseURL bool        `yaml:"allow_custom_base_url" json:"allow_custom_base_url"`
	ModelTypes         []ModelType `yaml:"model_types,omitempty" json:"model_types,omitempty"`
	// ManagedBy identifies deployment-owned provider definitions. A non-empty
	// value means the provider is reconciled from the workspace provisioning
	// manifest and must not be modified through runtime APIs.
	ManagedBy string `yaml:"-" json:"managed_by,omitempty"`
}

// ParserProfile is the platform-owned document parser binding. Overrides are
// deployment-supplied parser values (e.g. mineru_endpoint) injected at parse
// time in enforce mode; their keys are locked so tenants cannot override
// them. LockedOverrideKeys locks additional keys without supplying a value.
// Runtime secret injection is intentionally out of scope (secrets stay in
// deployment-managed files and env vars referenced by ${NAME}).
type ParserProfile struct {	ID                 string            `yaml:"id" json:"id"`
	Engine             string            `yaml:"engine" json:"engine"`
	FileTypes          []string          `yaml:"file_types" json:"file_types"`
	Overrides          map[string]string `yaml:"overrides,omitempty" json:"overrides,omitempty"`
	LockedOverrideKeys []string          `yaml:"locked_override_keys,omitempty" json:"locked_override_keys,omitempty"`
}

// ModelGovernancePolicy is the deployment-facing effective policy document.
// Chat remains selectable because only ingestion purposes have fixed bindings.
type ModelGovernancePolicy struct {
	Mode                    ModelPolicyMode `yaml:"mode" json:"mode"`
	RequireExplicitProvider bool            `yaml:"require_explicit_provider" json:"require_explicit_provider"`
	FixedIngestEmbeddingID  string          `yaml:"fixed_ingest_embedding_id,omitempty" json:"fixed_ingest_embedding_id,omitempty"`
	FixedIngestSummaryID    string          `yaml:"fixed_ingest_summary_id,omitempty" json:"fixed_ingest_summary_id,omitempty"`
	FixedIngestVLMID        string          `yaml:"fixed_ingest_vlm_id,omitempty" json:"fixed_ingest_vlm_id,omitempty"`
	FixedIngestASRID        string          `yaml:"fixed_ingest_asr_id,omitempty" json:"fixed_ingest_asr_id,omitempty"`
	ParserProfile           *ParserProfile  `yaml:"parser_profile,omitempty" json:"parser_profile,omitempty"`
}
