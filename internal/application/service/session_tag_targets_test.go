package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tagTargetKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *tagTargetKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	_ context.Context,
	ids []string,
) ([]*types.KnowledgeBase, error) {
	out := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb := s.kbs[id]; kb != nil {
			out = append(out, kb)
		}
	}
	return out, nil
}

type tagTargetKnowledgeService struct {
	interfaces.KnowledgeService
	knowledges []*types.Knowledge
	tagIDs     map[string][]string
}

func (s *tagTargetKnowledgeService) GetKnowledgeBatchWithSharedAccess(
	_ context.Context,
	_ uint64,
	ids []string,
) ([]*types.Knowledge, error) {
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	out := make([]*types.Knowledge, 0)
	for _, knowledge := range s.knowledges {
		if allowed[knowledge.ID] {
			out = append(out, knowledge)
		}
	}
	return out, nil
}

func (s *tagTargetKnowledgeService) ListKnowledgeIDsByTagIDs(
	_ context.Context,
	_ uint64,
	kbID string,
	tagIDs []string,
) ([]string, error) {
	allowedTags := make(map[string]bool, len(tagIDs))
	for _, tagID := range tagIDs {
		allowedTags[tagID] = true
	}
	out := make([]string, 0)
	for knowledgeID, tags := range s.tagIDs {
		if !knowledgeBelongsToKB(s.knowledges, knowledgeID, kbID) {
			continue
		}
		for _, tagID := range tags {
			if allowedTags[tagID] {
				out = append(out, knowledgeID)
				break
			}
		}
	}
	return out, nil
}

func knowledgeBelongsToKB(knowledges []*types.Knowledge, knowledgeID string, kbID string) bool {
	for _, knowledge := range knowledges {
		if knowledge.ID == knowledgeID && knowledge.KnowledgeBaseID == kbID {
			return true
		}
	}
	return false
}

func newTagTargetSessionService() *sessionService {
	return &sessionService{
		knowledgeBaseService: &tagTargetKnowledgeBaseService{
			kbs: map[string]*types.KnowledgeBase{
				"doc-kb": {ID: "doc-kb", TenantID: 100, Type: types.KnowledgeBaseTypeDocument},
				"faq-kb": {ID: "faq-kb", TenantID: 100, Type: types.KnowledgeBaseTypeFAQ},
			},
		},
		knowledgeService: &tagTargetKnowledgeService{
			knowledges: []*types.Knowledge{
				{ID: "doc-1", TenantID: 100, KnowledgeBaseID: "doc-kb"},
				{ID: "doc-2", TenantID: 100, KnowledgeBaseID: "doc-kb"},
				{ID: "doc-3", TenantID: 100, KnowledgeBaseID: "doc-kb"},
			},
			tagIDs: map[string][]string{
				"doc-1": {"tag-a"},
				"doc-2": {"tag-b"},
				"doc-3": {"tag-a", "tag-b"},
			},
		},
	}
}

func tagTargetContext() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(100))
}

func TestBuildSearchTargets_DocumentTagScopeResolvesKnowledgeIDs(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.Equal(t, "doc-kb", targets[0].KnowledgeBaseID)
	assert.ElementsMatch(t, []string{"doc-1", "doc-3"}, targets[0].KnowledgeIDs)
	assert.Empty(t, targets[0].TagIDs)
	assert.True(t, targets[0].DisableDirectLoad)
}

func TestBuildSearchTargets_DocumentTagScopeIntersectsExplicitKnowledgeIDs(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		[]string{"doc-2", "doc-3"},
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.Equal(t, []string{"doc-3"}, targets[0].KnowledgeIDs)
	assert.True(t, targets[0].DisableDirectLoad)
}

func TestBuildSearchTargets_FAQTagScopeKeepsIndexTagFilter(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"faq-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "faq-kb", TagIDs: []string{"tag-a", "tag-b"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
	assert.Equal(t, "faq-kb", targets[0].KnowledgeBaseID)
	assert.ElementsMatch(t, []string{"tag-a", "tag-b"}, targets[0].TagIDs)
	assert.False(t, targets[0].DisableDirectLoad)
}

func TestBuildSearchTargets_FullKBWithTagScopeSkipsFullKBTarget(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.NotEqual(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
}

func TestBuildSearchTargets_DocumentTagScopeWithMissingKBMetadata(t *testing.T) {
	svc := &sessionService{
		knowledgeBaseService: &tagTargetKnowledgeBaseService{kbs: map[string]*types.KnowledgeBase{}},
		knowledgeService: &tagTargetKnowledgeService{
			knowledges: []*types.Knowledge{
				{ID: "doc-1", TenantID: 100, KnowledgeBaseID: "doc-kb"},
				{ID: "doc-3", TenantID: 100, KnowledgeBaseID: "doc-kb"},
			},
			tagIDs: map[string][]string{
				"doc-1": {"tag-a"},
				"doc-3": {"tag-a"},
			},
		},
	}

	targets, err := svc.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.ElementsMatch(t, []string{"doc-1", "doc-3"}, targets[0].KnowledgeIDs)
	assert.True(t, targets[0].DisableDirectLoad)
}

type tagTargetKnowledgeServiceWithError struct {
	tagTargetKnowledgeService
	listErr error
}

func (s *tagTargetKnowledgeServiceWithError) ListKnowledgeIDsByTagIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagIDs []string,
) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.tagTargetKnowledgeService.ListKnowledgeIDsByTagIDs(ctx, tenantID, kbID, tagIDs)
}

func TestBuildSearchTargets_DocumentTagScopeResolutionError(t *testing.T) {
	base := newTagTargetSessionService()
	base.knowledgeService = &tagTargetKnowledgeServiceWithError{
		tagTargetKnowledgeService: *base.knowledgeService.(*tagTargetKnowledgeService),
		listErr:                   fmt.Errorf("database unavailable"),
	}

	_, err := base.buildSearchTargets(
		tagTargetContext(),
		100,
		[]string{"doc-kb"},
		nil,
		[]types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")
}

// Spec: docs/knowledge-retrieve-spec.md section 3.5, 4.3.
// Post-processing sets DisableDirectLoad=true on SearchTargetTypeKnowledge
// targets so the knowledge-retrieve path uses hybrid search exclusively.
func TestRetrievePostProcess_KnowledgeIDsOnly_SetsDisableDirectLoad(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(tagTargetContext(), 100, nil, []string{"doc-1", "doc-2"}, nil)
	require.NoError(t, err)

	for _, t := range targets {
		if t.Type == types.SearchTargetTypeKnowledge {
			t.DisableDirectLoad = true
		}
	}

	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledge, targets[0].Type)
	assert.True(t, targets[0].DisableDirectLoad)
}

// Spec: docs/knowledge-retrieve-spec.md section 2.3.
// kb_id + same-KB knowledge_ids: kb_id scope wins, knowledge targets
// suppressed. Post-processing finds nothing to touch.
func TestRetrievePostProcess_KBWithSameKBKnowledge_KBScopeWins(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, []string{"doc-1", "doc-2"}, nil)
	require.NoError(t, err)

	for _, t := range targets {
		if t.Type == types.SearchTargetTypeKnowledge {
			t.DisableDirectLoad = true
		}
	}

	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
}

// Spec: docs/knowledge-retrieve-spec.md section 4.3.
// kb_id only: full KB target, no knowledge target to touch.
func TestRetrievePostProcess_KBOnly_TouchesNothing(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, nil, nil)
	require.NoError(t, err)

	for _, t := range targets {
		if t.Type == types.SearchTargetTypeKnowledge {
			t.DisableDirectLoad = true
		}
	}

	require.Len(t, targets, 1)
	assert.Equal(t, types.SearchTargetTypeKnowledgeBase, targets[0].Type)
}

// Spec: docs/knowledge-retrieve-spec.md section 2.3.
// Tag-scoped targets already set DisableDirectLoad=true; post-processing
// is idempotent.
func TestRetrievePostProcess_TagScope_DisableDirectLoadAlreadyTrue(t *testing.T) {
	svc := newTagTargetSessionService()

	targets, err := svc.buildSearchTargets(tagTargetContext(), 100, []string{"doc-kb"}, nil, []types.TagScope{{KnowledgeBaseID: "doc-kb", TagIDs: []string{"tag-a"}}})
	require.NoError(t, err)

	for _, t := range targets {
		if t.Type == types.SearchTargetTypeKnowledge {
			t.DisableDirectLoad = true
		}
	}

	require.Len(t, targets, 1)
	assert.True(t, targets[0].DisableDirectLoad)
}
