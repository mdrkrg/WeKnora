package types

import "time"

const (
	ArtifactTypeMarkdown      = "markdown"
	ArtifactTypeImageManifest = "image_manifest"
	ArtifactTypeEngineNative  = "engine_native"
)

const EngineManual = "manual"

// KnowledgeArtifact represents a persisted parse artifact row.
type KnowledgeArtifact struct {
	ID           string    `json:"id"            gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id"`
	KnowledgeID  string    `json:"knowledge_id"`
	Attempt      int       `json:"attempt"`
	ArtifactType string    `json:"artifact_type"`
	NativeKind   string    `json:"native_kind,omitempty"`
	Engine       string    `json:"engine"`
	Format       string    `json:"format"`
	Size         int64     `json:"size"`
	Sha256       string    `json:"sha256"`
	StorageKey   string    `json:"storage_key"`
	CreatedAt    time.Time `json:"created_at"`
}

// ArtifactReadRequest holds the query parameters for reading an artifact.
type ArtifactReadRequest struct {
	Type          string `form:"type"`
	NativeKind    string `form:"native_kind"`
	Attempt       int    `form:"attempt"`
	ResolveImages bool   `form:"resolve_images"`
}

// ArtifactListRequest holds the query parameters for listing artifacts.
type ArtifactListRequest struct {
	Attempt int `form:"attempt"`
}

// ArtifactReadResponse is the JSON body returned for artifact read.
type ArtifactReadResponse struct {
	KnowledgeID  string `json:"knowledge_id"`
	ParseAttempt int    `json:"parse_attempt"`
	Engine       string `json:"engine"`
	ArtifactType string `json:"artifact_type"`
	NativeKind   string `json:"native_kind,omitempty"`
	Format       string `json:"format"`
	Sha256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Content      string `json:"content,omitempty"`
}

// ArtifactListItem is a single entry in the artifact list response.
type ArtifactListItem struct {
	ArtifactType string `json:"artifact_type"`
	NativeKind   string `json:"native_kind,omitempty"`
	Format       string `json:"format"`
	Sha256       string `json:"sha256"`
	Size         int64  `json:"size"`
	CreatedAt    string `json:"created_at"`
}
