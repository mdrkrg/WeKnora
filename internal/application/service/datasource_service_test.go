package service

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncCancelsWhenKnowledgeBaseDeleted(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-deleted",
		Type:            types.ConnectorTypeRSS,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo("kb-deleted", ds)
	syncLog := &types.SyncLog{
		ID:           "log-1",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}

	svc := &DataSourceService{
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		kbService:   &processSyncKBService{getErr: apprepo.ErrKnowledgeBaseNotFound},
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.NoError(t, err)

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, types.SyncLogStatusCanceled, updated.Status)
	assert.Equal(t, "knowledge base has been deleted", updated.ErrorMessage)
	require.NotNil(t, updated.FinishedAt)
}

type processSyncKBService struct {
	getErr error
	kb     *types.KnowledgeBase
}

func (s *processSyncKBService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, s.getErr
}
func (s *processSyncKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, s.getErr
}
func (s *processSyncKBService) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) FillKnowledgeBaseCounts(context.Context, *types.KnowledgeBase) error {
	return nil
}
func (s *processSyncKBService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) ListKnowledgeBasesByTenantID(context.Context, uint64) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) UpdateKnowledgeBase(
	context.Context, string, string, string, *types.KnowledgeBaseConfig,
) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) DeleteKnowledgeBase(context.Context, string) error { return nil }
func (s *processSyncKBService) TogglePinKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, nil
}
func (s *processSyncKBService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	return nil, nil
}
func (s *processSyncKBService) ResolveEmbeddingModelKeys(context.Context, []*types.KnowledgeBase) map[string]string {
	return nil
}
func (s *processSyncKBService) CopyKnowledgeBase(context.Context, string, string) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	return nil, nil, nil
}
func (s *processSyncKBService) DuplicateKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetRepository() interfaces.KnowledgeBaseRepository { return nil }
func (s *processSyncKBService) ProcessKBDelete(context.Context, *asynq.Task) error {
	return nil
}

var _ interfaces.KnowledgeBaseService = (*processSyncKBService)(nil)

type processSyncSyncLogRepo struct {
	logs map[string]*types.SyncLog
}

func (r *processSyncSyncLogRepo) Create(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}
func (r *processSyncSyncLogRepo) FindByID(_ context.Context, id string) (*types.SyncLog, error) {
	log, ok := r.logs[id]
	if !ok {
		return nil, errors.New("sync log not found")
	}
	return log, nil
}
func (r *processSyncSyncLogRepo) FindByDataSource(context.Context, string, int, int) ([]*types.SyncLog, error) {
	return nil, nil
}
func (r *processSyncSyncLogRepo) FindLatest(context.Context, string) (*types.SyncLog, error) {
	return nil, nil
}
func (r *processSyncSyncLogRepo) HasRunningSync(context.Context, string) (bool, error) {
	return false, nil
}
func (r *processSyncSyncLogRepo) Update(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}
func (r *processSyncSyncLogRepo) UpdateResult(_ context.Context, log *types.SyncLog) error {
	return r.Update(context.Background(), log)
}
func (r *processSyncSyncLogRepo) CancelPendingByDataSource(context.Context, string) error {
	return nil
}
func (r *processSyncSyncLogRepo) CleanupOldLogs(context.Context, int) error { return nil }

func TestAllFetchedItemsFailedError(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  2,
		Failed: 2,
		Errors: []types.SyncItemError{{Message: "doc one: export failed"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all fetched items failed during sync (2/2)")
	assert.Contains(t, err.Error(), "doc one: export failed")
}

func TestAllFetchedItemsFailedErrorIgnoresPartialFailure(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Created: 1,
		Failed:  2,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorIgnoresSkippedItems(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Skipped: 3,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorTruncatesLongDetail(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  1,
		Failed: 1,
		Errors: []types.SyncItemError{{Message: strings.Repeat("x", 600)}},
	})
	require.Error(t, err)
	assert.LessOrEqual(t, len(err.Error()), 560)
	assert.Contains(t, err.Error(), "...")
}

const deletedItemConnectorType = types.ConnectorTypeCanvas
const skippedItemConnectorType = "test-skipped-item"

type deletedItemConnector struct{}

type skippedItemConnector struct{}

func (skippedItemConnector) Type() string                                            { return skippedItemConnectorType }
func (skippedItemConnector) Validate(context.Context, *types.DataSourceConfig) error { return nil }
func (skippedItemConnector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}
func (skippedItemConnector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return nil, nil
}
func (skippedItemConnector) FetchAll(context.Context, *types.DataSourceConfig, []string) ([]types.FetchedItem, error) {
	return []types.FetchedItem{{ExternalID: "file:unchanged", IsSkipped: true}}, nil
}
func (skippedItemConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, err := (skippedItemConnector{}).FetchAll(context.Background(), nil, nil)
	return items, nil, err
}

func (deletedItemConnector) Type() string { return deletedItemConnectorType }
func (deletedItemConnector) Validate(context.Context, *types.DataSourceConfig) error {
	return nil
}
func (deletedItemConnector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}
func (deletedItemConnector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return nil, nil
}
func (deletedItemConnector) FetchAll(context.Context, *types.DataSourceConfig, []string) ([]types.FetchedItem, error) {
	return []types.FetchedItem{{
		ExternalID:       "file:gone",
		SourceResourceID: "folder:1",
		IsDeleted:        true,
	}}, nil
}
func (deletedItemConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, err := (deletedItemConnector{}).FetchAll(context.Background(), nil, nil)
	return items, nil, err
}

type processSyncTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r *processSyncTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return r.tenant, nil
}

type processSyncTagService struct {
	interfaces.KnowledgeTagService
}

func (*processSyncTagService) FindOrCreateTagByName(context.Context, string, string) (*types.KnowledgeTag, error) {
	return nil, nil
}

type deletionTrackingKnowledgeService struct {
	interfaces.KnowledgeService
	repo       interfaces.KnowledgeRepository
	deletedIDs []string
}

func (s *deletionTrackingKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *deletionTrackingKnowledgeService) DeleteKnowledge(_ context.Context, id string) error {
	s.deletedIDs = append(s.deletedIDs, id)
	return nil
}

type deletionLookupKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge       *types.Knowledge
	tenantID        uint64
	knowledgeBaseID string
	dataSourceID    string
	externalID      string
}

func (r *deletionLookupKnowledgeRepo) FindByDataSourceExternalID(
	_ context.Context, tenantID uint64, knowledgeBaseID, dataSourceID, externalID string,
) (*types.Knowledge, error) {
	r.tenantID = tenantID
	r.knowledgeBaseID = knowledgeBaseID
	r.dataSourceID = dataSourceID
	r.externalID = externalID
	return r.knowledge, nil
}

func TestProcessSync_SyncDeletionsDeletesMatchingKnowledge(t *testing.T) {
	configJSON, err := (&types.DataSourceConfig{Type: deletedItemConnectorType}).ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID:              "ds-delete-characterization",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Canvas",
		Type:            deletedItemConnectorType,
		Config:          configJSON,
		SyncMode:        types.SyncModeFull,
		Status:          types.DataSourceStatusActive,
		SyncDeletions:   true,
	}
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	syncLog := &types.SyncLog{
		ID:           "log-delete-characterization",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(deletedItemConnector{}))
	knowledgeRepo := &deletionLookupKnowledgeRepo{knowledge: &types.Knowledge{ID: "knowledge-gone"}}
	knowledgeSvc := &deletionTrackingKnowledgeService{repo: knowledgeRepo}

	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		knowledgeService:  knowledgeSvc,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID}},
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &processSyncTagService{},
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
		ForceFull:    true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, 1, updated.ItemsDeleted)
	assert.Equal(t, []string{"knowledge-gone"}, knowledgeSvc.deletedIDs)
	assert.Equal(t, ds.TenantID, knowledgeRepo.tenantID)
	assert.Equal(t, ds.KnowledgeBaseID, knowledgeRepo.knowledgeBaseID)
	assert.Equal(t, ds.ID, knowledgeRepo.dataSourceID)
	assert.Equal(t, "file:gone", knowledgeRepo.externalID)
}

func TestProcessSync_CountsConnectorReportedSkippedItems(t *testing.T) {
	configJSON, err := (&types.DataSourceConfig{Type: skippedItemConnectorType}).ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID:              "ds-skipped",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Canvas",
		Type:            skippedItemConnectorType,
		Config:          configJSON,
		SyncMode:        types.SyncModeIncremental,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	syncLog := &types.SyncLog{
		ID:           "log-skipped",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(skippedItemConnector{}))

	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID}},
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &processSyncTagService{},
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, types.SyncLogStatusSuccess, updated.Status)
	assert.Equal(t, 1, updated.ItemsTotal)
	assert.Equal(t, 1, updated.ItemsSkipped)
	assert.Zero(t, updated.ItemsCreated)
	assert.Zero(t, updated.ItemsUpdated)
	assert.Zero(t, updated.ItemsFailed)
}

type canvasMissingItemConnector struct {
	refetchedResourceIDs []string
	refetchEmpty         bool
}

func (*canvasMissingItemConnector) Type() string { return types.ConnectorTypeCanvas }
func (*canvasMissingItemConnector) Validate(context.Context, *types.DataSourceConfig) error {
	return nil
}
func (*canvasMissingItemConnector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}
func (*canvasMissingItemConnector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return nil, nil
}
func (c *canvasMissingItemConnector) FetchAll(
	_ context.Context, _ *types.DataSourceConfig, resourceIDs []string,
) ([]types.FetchedItem, error) {
	c.refetchedResourceIDs = append([]string(nil), resourceIDs...)
	if c.refetchEmpty {
		return nil, nil
	}
	return []types.FetchedItem{{
		ExternalID:       "file:missing",
		Title:            "missing.pdf",
		FileName:         "missing.pdf",
		Content:          []byte("restored"),
		SourceResourceID: "file:missing",
	}}, nil
}
func (*canvasMissingItemConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return []types.FetchedItem{
		{ExternalID: "file:present", SourceResourceID: "course:1", IsSkipped: true},
		{ExternalID: "file:missing", SourceResourceID: "course:1", IsSkipped: true},
	}, nil, nil
}

type canvasRecoveryKnowledgeRepo struct {
	interfaces.KnowledgeRepository
}

func (*canvasRecoveryKnowledgeRepo) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _, _, externalID string,
) (*types.Knowledge, error) {
	if externalID == "file:present" {
		return &types.Knowledge{ID: "knowledge-present"}, nil
	}
	return nil, nil
}

type canvasRecoveryKnowledgeService struct {
	interfaces.KnowledgeService
	repo            interfaces.KnowledgeRepository
	createdMetadata []map[string]string
}

func (s *canvasRecoveryKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *canvasRecoveryKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	_ *multipart.FileHeader,
	metadata map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.createdMetadata = append(s.createdMetadata, metadata)
	return &types.Knowledge{ID: "knowledge-restored"}, nil
}

func TestProcessSync_RestoresManuallyDeletedCanvasItem(t *testing.T) {
	configJSON, err := (&types.DataSourceConfig{
		Type:        types.ConnectorTypeCanvas,
		ResourceIDs: []string{"course:1"},
	}).ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID:              "ds-canvas-recovery",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "Canvas",
		Type:            types.ConnectorTypeCanvas,
		Config:          configJSON,
		SyncMode:        types.SyncModeIncremental,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	syncLog := &types.SyncLog{
		ID:           "log-canvas-recovery",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	connector := &canvasMissingItemConnector{}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(connector))
	knowledgeService := &canvasRecoveryKnowledgeService{repo: &canvasRecoveryKnowledgeRepo{}}

	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		knowledgeService:  knowledgeService,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID}},
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
		tagService:        &processSyncTagService{},
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, types.SyncLogStatusSuccess, updated.Status)
	assert.Equal(t, 2, updated.ItemsTotal)
	assert.Equal(t, 1, updated.ItemsCreated)
	assert.Equal(t, 1, updated.ItemsSkipped)
	assert.Zero(t, updated.ItemsUpdated)
	assert.Zero(t, updated.ItemsFailed)
	assert.Equal(t, []string{"file:missing"}, connector.refetchedResourceIDs)
	require.Len(t, knowledgeService.createdMetadata, 1)
	assert.Equal(t, "file:missing", knowledgeService.createdMetadata[0]["external_id"])
	assert.Equal(t, "course:1", knowledgeService.createdMetadata[0]["source_resource_id"])
	assert.Equal(t, ds.ID, knowledgeService.createdMetadata[0]["datasource_id"])
}

func TestRehydrateMissingCanvasItems_ErrorsWhenRefetchReturnsNoItem(t *testing.T) {
	connector := &canvasMissingItemConnector{refetchEmpty: true}
	svc := &DataSourceService{knowledgeService: &canvasRecoveryKnowledgeService{repo: &canvasRecoveryKnowledgeRepo{}}}
	ds := &types.DataSource{
		ID: "ds-canvas-recovery", TenantID: 1, KnowledgeBaseID: "kb-1", Type: types.ConnectorTypeCanvas,
	}

	_, err := svc.rehydrateMissingCanvasItems(
		context.Background(),
		ds,
		connector,
		&types.DataSourceConfig{Type: types.ConnectorTypeCanvas},
		[]types.FetchedItem{{ExternalID: "file:missing", IsSkipped: true}},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "file:missing returned no content")
}
