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
	ID                 string      `json:"id"`
	DisplayName        string      `json:"display_name"`
	Description        string      `json:"description,omitempty"`
	Adapter            string      `json:"adapter"`
	BaseURL            string      `json:"base_url,omitempty"`
	Approved           bool        `json:"approved"`
	AllowCustomBaseURL bool        `json:"allow_custom_base_url"`
	ModelTypes         []ModelType `json:"model_types,omitempty"`
	// ManagedBy identifies deployment-owned provider definitions. A non-empty
	// value means the provider is reconciled from the workspace provisioning
	// manifest and must not be modified through runtime APIs.
	ManagedBy string `yaml:"-" json:"managed_by,omitempty"`
}

// ParserProfile is the platform-owned document parser binding. LockedOverrideKeys
// are override keys tenants may no longer change; runtime secret injection is
// intentionally out of scope (secrets stay in deployment-managed files).
type ParserProfile struct {
	ID                 string   `json:"id"`
	Engine             string   `json:"engine"`
	FileTypes          []string `json:"file_types"`
	LockedOverrideKeys []string `json:"locked_override_keys,omitempty"`
}

// ModelGovernancePolicy is the deployment-facing effective policy document.
// Chat remains selectable because only ingestion purposes have fixed bindings.
type ModelGovernancePolicy struct {
	Mode                    ModelPolicyMode `json:"mode"`
	RequireExplicitProvider bool            `json:"require_explicit_provider"`
	FixedIngestEmbeddingID  string          `json:"fixed_ingest_embedding_id,omitempty"`
	FixedIngestSummaryID    string          `json:"fixed_ingest_summary_id,omitempty"`
	FixedIngestVLMID        string          `json:"fixed_ingest_vlm_id,omitempty"`
	FixedIngestASRID        string          `json:"fixed_ingest_asr_id,omitempty"`
	ParserProfile           *ParserProfile  `json:"parser_profile,omitempty"`
}
