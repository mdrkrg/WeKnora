package service

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubArtifactRepo captures artifact repository calls for white-box tests.
type stubArtifactRepo struct {
	interfaces.KnowledgeArtifactRepository
	setCurrentAttemptCalls []int
	createCalls            []*types.KnowledgeArtifact
	listCalls              []int
	listResult             map[int][]types.KnowledgeArtifact
	deleteByAttemptCalls   []int
	deleteByTypeCalls      []string
	deleteByKnowledgeCalls int
	existing               *types.KnowledgeArtifact // returned by GetArtifactByType when matching
}

func (s *stubArtifactRepo) CreateArtifact(_ context.Context, a *types.KnowledgeArtifact) error {
	s.createCalls = append(s.createCalls, a)
	return nil
}

func (s *stubArtifactRepo) SetCurrentAttempt(_ context.Context, _ string, attempt int) error {
	s.setCurrentAttemptCalls = append(s.setCurrentAttemptCalls, attempt)
	return nil
}

func (s *stubArtifactRepo) ListArtifacts(_ context.Context, _ uint64, _ string, attempt int) ([]types.KnowledgeArtifact, error) {
	s.listCalls = append(s.listCalls, attempt)
	if s.listResult != nil {
		return s.listResult[attempt], nil
	}
	return nil, nil
}

func (s *stubArtifactRepo) DeleteArtifactsByAttempt(_ context.Context, _ uint64, _ string, attempt int) (int64, error) {
	s.deleteByAttemptCalls = append(s.deleteByAttemptCalls, attempt)
	return 0, nil
}

func (s *stubArtifactRepo) DeleteArtifactsByKnowledgeID(_ context.Context, _ uint64, _ string) (int64, error) {
	s.deleteByKnowledgeCalls++
	return 0, nil
}

func (s *stubArtifactRepo) DeleteArtifactByType(_ context.Context, _ uint64, _ string, _ int, artifactType, _ string) (int64, error) {
	s.deleteByTypeCalls = append(s.deleteByTypeCalls, artifactType)
	return 0, nil
}

func (s *stubArtifactRepo) GetArtifactByType(_ context.Context, _ uint64, _ string, _ int, artifactType, _ string) (*types.KnowledgeArtifact, error) {
	if s.existing != nil && s.existing.ArtifactType == artifactType {
		return s.existing, nil
	}
	return nil, repository.ErrArtifactNotFound
}

// stubFileSvcForArtifacts implements just the file operations the artifact
// code paths need.
type stubFileSvcForArtifacts struct {
	interfaces.FileService
	deleteCalls []string
}

func (s *stubFileSvcForArtifacts) SaveBytes(_ context.Context, data []byte, _ uint64, fileName string, _ bool) (string, error) {
	return "local://fake/" + fileName, nil
}

func (s *stubFileSvcForArtifacts) DeleteFile(_ context.Context, key string) error {
	s.deleteCalls = append(s.deleteCalls, key)
	return nil
}

func (s *stubFileSvcForArtifacts) GetFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *stubFileSvcForArtifacts) GetFileURL(_ context.Context, p string) (string, error) {
	return p, nil
}
func (s *stubFileSvcForArtifacts) SaveFile(_ context.Context, _ *multipart.FileHeader, _ uint64, _ string) (string, error) {
	return "", nil
}
func (s *stubFileSvcForArtifacts) CheckConnectivity(_ context.Context) error { return nil }
func (s *stubFileSvcForArtifacts) CopyFile(_ context.Context, _ string, _ uint64, _ string) (string, error) {
	return "", nil
}

// stubTenantRepoForArtifacts implements just AdjustStorageUsed.
type stubTenantRepoForArtifacts struct {
	interfaces.TenantRepository
	adjustCalls []int64
}

func (s *stubTenantRepoForArtifacts) AdjustStorageUsed(_ context.Context, _ uint64, delta int64) error {
	s.adjustCalls = append(s.adjustCalls, delta)
	return nil
}

func artifactTestContext(tenant *types.Tenant) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	return context.WithValue(ctx, types.TenantIDContextKey, tenant.ID)
}

// TestSaveProcessArtifacts_RejectsZeroAttempt verifies that
// saveProcessArtifacts must not persist artifacts or commit the
// current attempt when attempt <= 0. Attempt 0 means "never parsed"
// in the read path; saving artifacts with attempt=0 makes them
// invisible because ListArtifacts/GetArtifactByType fall back to
// knowledge.CurrentAttempt (still 0) and return nil.
func TestSaveProcessArtifacts_RejectsZeroAttempt(t *testing.T) {
	artifactRepo := &stubArtifactRepo{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      &stubFileSvcForArtifacts{},
		tenantRepo:   &stubTenantRepoForArtifacts{},
	}

	tenant := &types.Tenant{ID: 10000}
	ctx := artifactTestContext(tenant)

	knowledge := &types.Knowledge{ID: "k1", TenantID: 10000}
	result := &types.ReadResult{
		MarkdownContent: "# hello",
		Metadata:        map[string]string{"resolved_engine": "mineru"},
	}

	err := svc.saveProcessArtifacts(ctx, knowledge, 0, result, nil, &types.EffectiveProcessConfig{})
	if err != nil {
		t.Fatalf("saveProcessArtifacts returned error: %v", err)
	}

	if len(artifactRepo.createCalls) > 0 {
		t.Errorf("expected no CreateArtifact calls with attempt=0, got %d", len(artifactRepo.createCalls))
	}
	if len(artifactRepo.setCurrentAttemptCalls) > 0 {
		t.Errorf("expected no SetCurrentAttempt calls with attempt=0, got %v", artifactRepo.setCurrentAttemptCalls)
	}
}

// TestSaveProcessArtifacts_ValidAttempt verifies that with a proper
// attempt number (>=1), artifacts are saved. The pointer commit is
// exercised separately via TestCommitCurrentAttempt* — saveProcessArtifacts
// itself must NOT advance current_attempt, because the parse may still fail
// in chunking/indexing after artifacts were persisted.
func TestSaveProcessArtifacts_ValidAttempt(t *testing.T) {
	artifactRepo := &stubArtifactRepo{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      &stubFileSvcForArtifacts{},
		tenantRepo:   &stubTenantRepoForArtifacts{},
	}

	tenant := &types.Tenant{ID: 10000}
	ctx := artifactTestContext(tenant)

	knowledge := &types.Knowledge{ID: "k1", TenantID: 10000}
	result := &types.ReadResult{
		MarkdownContent: "# hello",
		Metadata:        map[string]string{"resolved_engine": "mineru"},
	}

	err := svc.saveProcessArtifacts(ctx, knowledge, 1, result, nil, &types.EffectiveProcessConfig{})
	if err != nil {
		t.Fatalf("saveProcessArtifacts returned error: %v", err)
	}

	if len(artifactRepo.createCalls) != 2 {
		t.Fatalf("expected 2 CreateArtifact calls (markdown + image_manifest), got %d", len(artifactRepo.createCalls))
	}
	for _, a := range artifactRepo.createCalls {
		if a.Attempt != 1 {
			t.Errorf("expected attempt=1 on artifact, got %d", a.Attempt)
		}
	}
	if len(artifactRepo.setCurrentAttemptCalls) != 0 {
		t.Errorf("saveProcessArtifacts must not commit the attempt, got SetCurrentAttempt calls %v", artifactRepo.setCurrentAttemptCalls)
	}
}

// TestCommitCurrentAttempt_AdvancesAndPrunes verifies that a successful
// parse advances the artifact pointer and prunes attempts strictly older
// than the previously committed attempt, refunding their quota.
func TestCommitCurrentAttempt_AdvancesAndPrunes(t *testing.T) {
	artifactRepo := &stubArtifactRepo{
		listResult: map[int][]types.KnowledgeArtifact{
			1: {{ID: "a1", StorageKey: "local://artifacts/k1/1/markdown", Size: 100}},
		},
	}
	fileSvc := &stubFileSvcForArtifacts{}
	tenantRepo := &stubTenantRepoForArtifacts{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      fileSvc,
		tenantRepo:   tenantRepo,
	}

	tenant := &types.Tenant{ID: 10000, StorageUsed: 500}
	ctx := artifactTestContext(tenant)
	knowledge := &types.Knowledge{ID: "k1", CurrentAttempt: 2}

	svc.commitCurrentAttempt(ctx, knowledge, 3)

	if len(artifactRepo.setCurrentAttemptCalls) != 1 || artifactRepo.setCurrentAttemptCalls[0] != 3 {
		t.Fatalf("expected SetCurrentAttempt(3), got %v", artifactRepo.setCurrentAttemptCalls)
	}
	if knowledge.CurrentAttempt != 3 {
		t.Errorf("expected knowledge.CurrentAttempt=3, got %d", knowledge.CurrentAttempt)
	}
	if len(artifactRepo.listCalls) != 1 || artifactRepo.listCalls[0] != 1 {
		t.Errorf("expected retention to list attempt 1, got %v", artifactRepo.listCalls)
	}
	if len(fileSvc.deleteCalls) != 1 || fileSvc.deleteCalls[0] != "local://artifacts/k1/1/markdown" {
		t.Errorf("expected stale artifact file deletion, got %v", fileSvc.deleteCalls)
	}
	if len(artifactRepo.deleteByAttemptCalls) != 1 || artifactRepo.deleteByAttemptCalls[0] != 1 {
		t.Errorf("expected DeleteArtifactsByAttempt(1), got %v", artifactRepo.deleteByAttemptCalls)
	}
	if len(tenantRepo.adjustCalls) != 1 || tenantRepo.adjustCalls[0] != -100 {
		t.Errorf("expected storage refund of -100, got %v", tenantRepo.adjustCalls)
	}
	if tenant.StorageUsed != 400 {
		t.Errorf("expected in-memory StorageUsed=400, got %d", tenant.StorageUsed)
	}
}

// TestCommitCurrentAttempt_KeepsPreviousAttempt verifies attempt 2 keeps
// attempt 1 (oldestToKeep=1 → nothing pruned).
func TestCommitCurrentAttempt_KeepsPreviousAttempt(t *testing.T) {
	artifactRepo := &stubArtifactRepo{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      &stubFileSvcForArtifacts{},
		tenantRepo:   &stubTenantRepoForArtifacts{},
	}
	ctx := artifactTestContext(&types.Tenant{ID: 10000})
	knowledge := &types.Knowledge{ID: "k1", CurrentAttempt: 1}

	svc.commitCurrentAttempt(ctx, knowledge, 2)

	if len(artifactRepo.setCurrentAttemptCalls) != 1 || artifactRepo.setCurrentAttemptCalls[0] != 2 {
		t.Fatalf("expected SetCurrentAttempt(2), got %v", artifactRepo.setCurrentAttemptCalls)
	}
	if len(artifactRepo.deleteByAttemptCalls) != 0 {
		t.Errorf("expected no retention deletion for attempt 2, got %v", artifactRepo.deleteByAttemptCalls)
	}
}

// TestCommitCurrentAttempt_SkipsInvalidAttempt verifies attempt <= 0 never
// advances the pointer.
func TestCommitCurrentAttempt_SkipsInvalidAttempt(t *testing.T) {
	artifactRepo := &stubArtifactRepo{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      &stubFileSvcForArtifacts{},
		tenantRepo:   &stubTenantRepoForArtifacts{},
	}
	ctx := artifactTestContext(&types.Tenant{ID: 10000})
	knowledge := &types.Knowledge{ID: "k1"}

	svc.commitCurrentAttempt(ctx, knowledge, 0)

	if len(artifactRepo.setCurrentAttemptCalls) != 0 {
		t.Errorf("expected no SetCurrentAttempt for attempt=0, got %v", artifactRepo.setCurrentAttemptCalls)
	}
	if len(artifactRepo.listCalls) != 0 {
		t.Errorf("expected no retention for attempt=0, got %v", artifactRepo.listCalls)
	}
}

// TestCommitCurrentAttempt_KeepsPreviousVersionAcrossFailedGap verifies the
// retention window derives from the previously committed attempt, not the new
// attempt number: a failed attempt 2 (partial leftovers, no commit) followed
// by success 3 must keep the readable attempt 1 instead of evicting it.
func TestCommitCurrentAttempt_KeepsPreviousVersionAcrossFailedGap(t *testing.T) {
	artifactRepo := &stubArtifactRepo{
		listResult: map[int][]types.KnowledgeArtifact{
			1: {{ID: "a1", StorageKey: "local://artifacts/k1/1/markdown", Size: 100}},
		},
	}
	fileSvc := &stubFileSvcForArtifacts{}
	tenantRepo := &stubTenantRepoForArtifacts{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      fileSvc,
		tenantRepo:   tenantRepo,
	}
	tenant := &types.Tenant{ID: 10000, StorageUsed: 500}
	ctx := artifactTestContext(tenant)
	knowledge := &types.Knowledge{ID: "k1", CurrentAttempt: 1}

	svc.commitCurrentAttempt(ctx, knowledge, 3)

	if len(artifactRepo.setCurrentAttemptCalls) != 1 || artifactRepo.setCurrentAttemptCalls[0] != 3 {
		t.Fatalf("expected SetCurrentAttempt(3), got %v", artifactRepo.setCurrentAttemptCalls)
	}
	if len(artifactRepo.listCalls) != 0 {
		t.Errorf("expected no retention listing, got %v", artifactRepo.listCalls)
	}
	if len(artifactRepo.deleteByAttemptCalls) != 0 {
		t.Errorf("expected no retention deletion, got %v", artifactRepo.deleteByAttemptCalls)
	}
	if len(fileSvc.deleteCalls) != 0 {
		t.Errorf("expected no artifact file deletion, got %v", fileSvc.deleteCalls)
	}
	if tenant.StorageUsed != 500 {
		t.Errorf("expected StorageUsed unchanged, got %d", tenant.StorageUsed)
	}
}

// TestCleanupFailedAttempt_RemovesUncommittedArtifacts verifies a parse
// attempt that never committed (attempt > committed current) has its rows,
// files and quota refunded.
func TestCleanupFailedAttempt_RemovesUncommittedArtifacts(t *testing.T) {
	artifactRepo := &stubArtifactRepo{
		listResult: map[int][]types.KnowledgeArtifact{
			5: {
				{ID: "p1", StorageKey: "local://artifacts/k1/5/markdown", Size: 100},
				{ID: "p2", StorageKey: "", Size: 50},
			},
		},
	}
	fileSvc := &stubFileSvcForArtifacts{}
	tenantRepo := &stubTenantRepoForArtifacts{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      fileSvc,
		tenantRepo:   tenantRepo,
	}
	tenant := &types.Tenant{ID: 10000, StorageUsed: 400}
	ctx := artifactTestContext(tenant)
	knowledge := &types.Knowledge{ID: "k1", CurrentAttempt: 2}

	svc.cleanupFailedAttempt(ctx, knowledge, 5)

	if len(artifactRepo.deleteByAttemptCalls) != 1 || artifactRepo.deleteByAttemptCalls[0] != 5 {
		t.Errorf("expected DeleteArtifactsByAttempt(5), got %v", artifactRepo.deleteByAttemptCalls)
	}
	if len(fileSvc.deleteCalls) != 1 || fileSvc.deleteCalls[0] != "local://artifacts/k1/5/markdown" {
		t.Errorf("expected partial artifact file deletion, got %v", fileSvc.deleteCalls)
	}
	if len(tenantRepo.adjustCalls) != 1 || tenantRepo.adjustCalls[0] != -150 {
		t.Errorf("expected storage refund of -150, got %v", tenantRepo.adjustCalls)
	}
	if tenant.StorageUsed != 250 {
		t.Errorf("expected in-memory StorageUsed=250, got %d", tenant.StorageUsed)
	}
}

// TestCleanupFailedAttempt_SkipsCommittedOrEmptyAttempts verifies the cleanup
// is a no-op for attempts that committed (or were superseded by a later
// commit) and for attempts without artifacts.
func TestCleanupFailedAttempt_SkipsCommittedOrEmptyAttempts(t *testing.T) {
	artifactRepo := &stubArtifactRepo{}
	fileSvc := &stubFileSvcForArtifacts{}
	tenantRepo := &stubTenantRepoForArtifacts{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      fileSvc,
		tenantRepo:   tenantRepo,
	}
	ctx := artifactTestContext(&types.Tenant{ID: 10000})
	knowledge := &types.Knowledge{ID: "k1", CurrentAttempt: 3}

	svc.cleanupFailedAttempt(ctx, knowledge, 3)
	svc.cleanupFailedAttempt(ctx, knowledge, 2)
	svc.cleanupFailedAttempt(ctx, knowledge, 0)
	svc.cleanupFailedAttempt(ctx, knowledge, 4) // > committed, but no artifacts

	if len(artifactRepo.listCalls) != 1 {
		t.Errorf("expected a single ListArtifacts call (attempt 4), got %v", artifactRepo.listCalls)
	}
	if len(artifactRepo.deleteByAttemptCalls) != 0 {
		t.Errorf("expected no deletion, got %v", artifactRepo.deleteByAttemptCalls)
	}
	if len(tenantRepo.adjustCalls) != 0 {
		t.Errorf("expected no storage adjustment, got %v", tenantRepo.adjustCalls)
	}
}

// TestCleanupKnowledgeArtifacts verifies delete-time cleanup removes rows,
// files and refunds the tenant quota for all attempts of a knowledge.
func TestCleanupKnowledgeArtifacts(t *testing.T) {
	artifactRepo := &stubArtifactRepo{
		listResult: map[int][]types.KnowledgeArtifact{
			0: {
				{ID: "a1", StorageKey: "local://a/1", Size: 10},
				{ID: "a2", StorageKey: "local://a/2", Size: 20},
			},
		},
	}
	fileSvc := &stubFileSvcForArtifacts{}
	tenantRepo := &stubTenantRepoForArtifacts{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      fileSvc,
		tenantRepo:   tenantRepo,
	}
	ctx := artifactTestContext(&types.Tenant{ID: 10000})

	svc.cleanupKnowledgeArtifacts(ctx, 10000, "k1")

	if artifactRepo.deleteByKnowledgeCalls != 1 {
		t.Errorf("expected DeleteArtifactsByKnowledgeID, got %d calls", artifactRepo.deleteByKnowledgeCalls)
	}
	if len(fileSvc.deleteCalls) != 2 {
		t.Errorf("expected 2 artifact file deletions, got %v", fileSvc.deleteCalls)
	}
	if len(tenantRepo.adjustCalls) != 1 || tenantRepo.adjustCalls[0] != -30 {
		t.Errorf("expected storage refund of -30, got %v", tenantRepo.adjustCalls)
	}
}

// TestSaveProcessArtifacts_ReplacesExistingArtifact verifies at-least-once
// redelivery: re-running the same attempt replaces the previous artifact of
// the same type (old file deleted, old row removed, quota refunded) instead
// of accumulating duplicates that double-charge quota.
func TestSaveProcessArtifacts_ReplacesExistingArtifact(t *testing.T) {
	artifactRepo := &stubArtifactRepo{
		existing: &types.KnowledgeArtifact{
			ID:           "old-md",
			ArtifactType: types.ArtifactTypeMarkdown,
			StorageKey:   "local://artifacts/k1/1/markdown",
			Size:         50,
		},
	}
	fileSvc := &stubFileSvcForArtifacts{}
	tenantRepo := &stubTenantRepoForArtifacts{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      fileSvc,
		tenantRepo:   tenantRepo,
	}

	tenant := &types.Tenant{ID: 10000, StorageUsed: 200}
	ctx := artifactTestContext(tenant)

	knowledge := &types.Knowledge{ID: "k1", TenantID: 10000}
	result := &types.ReadResult{
		MarkdownContent: "# hello",
		Metadata:        map[string]string{"resolved_engine": "mineru"},
	}

	err := svc.saveProcessArtifacts(ctx, knowledge, 1, result, nil, &types.EffectiveProcessConfig{})
	if err != nil {
		t.Fatalf("saveProcessArtifacts returned error: %v", err)
	}

	if len(fileSvc.deleteCalls) != 1 || fileSvc.deleteCalls[0] != "local://artifacts/k1/1/markdown" {
		t.Errorf("expected replaced markdown file deletion, got %v", fileSvc.deleteCalls)
	}
	if len(artifactRepo.deleteByTypeCalls) != 1 || artifactRepo.deleteByTypeCalls[0] != types.ArtifactTypeMarkdown {
		t.Errorf("expected DeleteArtifactByType(markdown), got %v", artifactRepo.deleteByTypeCalls)
	}
	// -50 refund for the replaced markdown, then +size charges for both saves.
	if len(tenantRepo.adjustCalls) != 3 || tenantRepo.adjustCalls[0] != -50 {
		t.Errorf("expected refund -50 then two charges, got %v", tenantRepo.adjustCalls)
	}
	if tenant.StorageUsed != 200-50+int64(len(result.MarkdownContent))+2 {
		t.Errorf("unexpected in-memory storage accounting: %d", tenant.StorageUsed)
	}
}

// artifactListKnowledgeRepo returns a fixed knowledge for ListArtifacts tests.
type artifactListKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *artifactListKnowledgeRepo) GetKnowledgeByID(
	_ context.Context,
	_ uint64,
	_ string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

// TestListArtifacts_NeverParsedReturnsEmptySlice verifies that a knowledge
// that was never parsed (CurrentAttempt=0) yields an empty, non-nil slice so
// the API returns [] instead of null.
func TestListArtifacts_NeverParsedReturnsEmptySlice(t *testing.T) {
	artifactRepo := &stubArtifactRepo{}
	svc := &knowledgeService{
		repo: &artifactListKnowledgeRepo{
			knowledge: &types.Knowledge{ID: "k1", TenantID: 10000, CurrentAttempt: 0},
		},
		artifactRepo: artifactRepo,
	}
	ctx := artifactTestContext(&types.Tenant{ID: 10000})

	result, err := svc.ListArtifacts(ctx, "k1", types.ArtifactListRequest{})
	if err != nil {
		t.Fatalf("ListArtifacts returned error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 items, got %d", len(result))
	}
}

// TestListArtifacts_ReturnsMappedItems verifies items of the current attempt
// are mapped to the API shape.
func TestListArtifacts_ReturnsMappedItems(t *testing.T) {
	createdAt := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	artifactRepo := &stubArtifactRepo{
		listResult: map[int][]types.KnowledgeArtifact{
			2: {
				{
					ArtifactType: types.ArtifactTypeMarkdown,
					Format:       "markdown",
					Sha256:       "abc",
					Size:         10,
					CreatedAt:    createdAt,
				},
				{
					ArtifactType: types.ArtifactTypeEngineNative,
					NativeKind:   "mineru",
					Format:       "json",
					Sha256:       "def",
					Size:         20,
					CreatedAt:    createdAt,
				},
			},
		},
	}
	svc := &knowledgeService{
		repo: &artifactListKnowledgeRepo{
			knowledge: &types.Knowledge{ID: "k1", TenantID: 10000, CurrentAttempt: 2},
		},
		artifactRepo: artifactRepo,
	}
	ctx := artifactTestContext(&types.Tenant{ID: 10000})

	result, err := svc.ListArtifacts(ctx, "k1", types.ArtifactListRequest{})
	if err != nil {
		t.Fatalf("ListArtifacts returned error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
	if len(artifactRepo.listCalls) != 1 || artifactRepo.listCalls[0] != 2 {
		t.Errorf("expected repo query with current attempt 2, got %v", artifactRepo.listCalls)
	}
	md := result[0]
	if md.ArtifactType != types.ArtifactTypeMarkdown || md.Format != "markdown" || md.Sha256 != "abc" || md.Size != 10 {
		t.Errorf("unexpected markdown item: %+v", md)
	}
	if md.CreatedAt != createdAt.Format(time.RFC3339) {
		t.Errorf("unexpected created_at: %q", md.CreatedAt)
	}
	native := result[1]
	if native.ArtifactType != types.ArtifactTypeEngineNative || native.NativeKind != "mineru" || native.Size != 20 {
		t.Errorf("unexpected engine_native item: %+v", native)
	}
}

// TestIsQuotaExceededError verifies the artifact-save error classifier
// distinguishes deterministic quota failures from transient ones.
func TestIsQuotaExceededError(t *testing.T) {
	if !isQuotaExceededError(fmt.Errorf("storage quota exceeded (used 100 + needed 50 > quota 120)")) {
		t.Error("expected quota-exceeded error to classify as permanent")
	}
	if isQuotaExceededError(fmt.Errorf("save artifact bytes: connection refused")) {
		t.Error("expected transient storage error NOT to classify as permanent")
	}
	if isQuotaExceededError(nil) {
		t.Error("expected nil error to classify as retryable/not permanent")
	}
}
