package service

import (
	"context"
	"io"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubArtifactRepo captures SetCurrentAttempt and CreateArtifact calls.
type stubArtifactRepo struct {
	interfaces.KnowledgeArtifactRepository
	setCurrentAttemptCalls []int
	createCalls             []*types.KnowledgeArtifact
}

func (s *stubArtifactRepo) CreateArtifact(_ context.Context, a *types.KnowledgeArtifact) error {
	s.createCalls = append(s.createCalls, a)
	return nil
}

func (s *stubArtifactRepo) SetCurrentAttempt(_ context.Context, _ string, attempt int) error {
	s.setCurrentAttemptCalls = append(s.setCurrentAttemptCalls, attempt)
	return nil
}

func (s *stubArtifactRepo) ListArtifacts(_ context.Context, _ uint64, _ string, _ int) ([]types.KnowledgeArtifact, error) {
	return nil, nil
}

func (s *stubArtifactRepo) DeleteArtifactsByAttempt(_ context.Context, _ uint64, _ string, _ int) (int64, error) {
	return 0, nil
}

// stubFileSvcForArtifacts implements just SaveBytes and DeleteFile.
type stubFileSvcForArtifacts struct {
	interfaces.FileService
}

func (s *stubFileSvcForArtifacts) SaveBytes(_ context.Context, data []byte, _ uint64, fileName string, _ bool) (string, error) {
	return "local://fake/" + fileName, nil
}

func (s *stubFileSvcForArtifacts) DeleteFile(_ context.Context, _ string) error { return nil }
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

// TestSaveProcessArtifacts_RejectsZeroAttempt verifies that
// saveProcessArtifacts must not persist artifacts or call
// SetCurrentAttempt when attempt <= 0. Attempt 0 means "never parsed"
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
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)

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
// attempt number (>=1), artifacts are saved and SetCurrentAttempt is
// called with the correct value.
func TestSaveProcessArtifacts_ValidAttempt(t *testing.T) {
	artifactRepo := &stubArtifactRepo{}
	svc := &knowledgeService{
		artifactRepo: artifactRepo,
		fileSvc:      &stubFileSvcForArtifacts{},
		tenantRepo:   &stubTenantRepoForArtifacts{},
	}

	tenant := &types.Tenant{ID: 10000}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)

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
	if len(artifactRepo.setCurrentAttemptCalls) != 1 || artifactRepo.setCurrentAttemptCalls[0] != 1 {
		t.Errorf("expected SetCurrentAttempt(1), got %v", artifactRepo.setCurrentAttemptCalls)
	}
}
