package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var (
	ErrArtifactNotFound = errors.New("artifact not found")
)

type knowledgeArtifactRepository struct {
	db *gorm.DB
}

func NewKnowledgeArtifactRepository(db *gorm.DB) interfaces.KnowledgeArtifactRepository {
	return &knowledgeArtifactRepository{db: db}
}

func (r *knowledgeArtifactRepository) CreateArtifact(ctx context.Context, artifact *types.KnowledgeArtifact) error {
	return r.db.WithContext(ctx).Create(artifact).Error
}

func (r *knowledgeArtifactRepository) GetArtifactByType(ctx context.Context, tenantID uint64, knowledgeID string, attempt int, artifactType, nativeKind string) (*types.KnowledgeArtifact, error) {
	var artifact types.KnowledgeArtifact
	q := r.db.WithContext(ctx).Where(
		"tenant_id = ? AND knowledge_id = ? AND attempt = ? AND artifact_type = ?",
		tenantID, knowledgeID, attempt, artifactType,
	)
	if nativeKind != "" {
		q = q.Where("native_kind = ?", nativeKind)
	}
	if err := q.First(&artifact).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrArtifactNotFound
		}
		return nil, err
	}
	return &artifact, nil
}

func (r *knowledgeArtifactRepository) ListArtifacts(ctx context.Context, tenantID uint64, knowledgeID string, attempt int) ([]types.KnowledgeArtifact, error) {
	var artifacts []types.KnowledgeArtifact
	q := r.db.WithContext(ctx).Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID)
	if attempt > 0 {
		q = q.Where("attempt = ?", attempt)
	}
	if err := q.Order("created_at ASC").Find(&artifacts).Error; err != nil {
		return nil, err
	}
	return artifacts, nil
}

func (r *knowledgeArtifactRepository) DeleteArtifactsByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID).
		Delete(&types.KnowledgeArtifact{})
	return result.RowsAffected, result.Error
}

func (r *knowledgeArtifactRepository) DeleteArtifactsByAttempt(ctx context.Context, tenantID uint64, knowledgeID string, attempt int) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("tenant_id = ? AND knowledge_id = ? AND attempt = ?", tenantID, knowledgeID, attempt).
		Delete(&types.KnowledgeArtifact{})
	return result.RowsAffected, result.Error
}

func (r *knowledgeArtifactRepository) GetCurrentAttempt(ctx context.Context, tenantID uint64, knowledgeID string) (int, error) {
	var knowledge types.Knowledge
	if err := r.db.WithContext(ctx).
		Select("current_attempt").
		Where("id = ? AND tenant_id = ?", knowledgeID, tenantID).
		First(&knowledge).Error; err != nil {
		return 0, err
	}
	return knowledge.CurrentAttempt, nil
}

func (r *knowledgeArtifactRepository) SetCurrentAttempt(ctx context.Context, knowledgeID string, attempt int) error {
	return r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("id = ?", knowledgeID).
		Update("current_attempt", attempt).Error
}
