package repository

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestArtifactBeforeCreateAssignsUUID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeArtifact{}))

	repo := NewKnowledgeArtifactRepository(db)

	a1 := &types.KnowledgeArtifact{
		TenantID:     1,
		KnowledgeID:  "k1",
		Attempt:      1,
		ArtifactType: types.ArtifactTypeMarkdown,
		Engine:       "mineru",
		Format:       "markdown",
		Size:         100,
		Sha256:       "abc",
		StorageKey:   "key1",
	}
	require.NoError(t, repo.CreateArtifact(t.Context(), a1))
	assert.NotEmpty(t, a1.ID, "BeforeCreate should assign a UUID")

	a2 := &types.KnowledgeArtifact{
		TenantID:     1,
		KnowledgeID:  "k1",
		Attempt:      1,
		ArtifactType: types.ArtifactTypeImageManifest,
		Engine:       "mineru",
		Format:       "json",
		Size:         50,
		Sha256:       "def",
		StorageKey:   "key2",
	}
	require.NoError(t, repo.CreateArtifact(t.Context(), a2))
	assert.NotEmpty(t, a2.ID, "BeforeCreate should assign a UUID")
	assert.NotEqual(t, a1.ID, a2.ID, "each artifact must get a distinct UUID")
}
