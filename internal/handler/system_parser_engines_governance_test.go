package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ssrfBlockedMinerUEndpoint is a deterministic MinerU endpoint for tests: the
// SSRF guard rejects it before any network I/O, so the availability probe
// result is stable and offline-friendly. Its presence in the engine list's
// mineru entry proves the platform override actually reached the check.
const ssrfBlockedMinerUEndpoint = "http://127.0.0.1:9"

// stubModelPolicyService is a minimal ModelPolicyService stand-in. Only
// ApplyPlatformParserOverrides is exercised by the parser-engine APIs; the
// remaining methods are no-ops.
type stubModelPolicyService struct {
	policy *types.ModelGovernancePolicy
}

var _ interfaces.ModelPolicyService = (*stubModelPolicyService)(nil)

func (s *stubModelPolicyService) GetPolicy(context.Context) (*types.ModelGovernancePolicy, error) {
	return s.policy, nil
}

func (s *stubModelPolicyService) ValidateModelForWrite(context.Context, *types.Model) error {
	return nil
}

func (s *stubModelPolicyService) PrepareModelForRuntime(context.Context, *types.Model) (*types.Model, error) {
	return nil, nil
}

func (s *stubModelPolicyService) FilterModelsForCaller(context.Context, []*types.Model) []*types.Model {
	return nil
}

func (s *stubModelPolicyService) ApplyKnowledgeBasePolicy(context.Context, *types.KnowledgeBase) error {
	return nil
}

func (s *stubModelPolicyService) ValidateProcessOverrides(
	context.Context, *types.KnowledgeBase, *types.KnowledgeProcessOverrides, []string,
) error {
	return nil
}

func (s *stubModelPolicyService) ApplyEffectiveProcessPolicy(context.Context, *types.EffectiveProcessConfig) error {
	return nil
}

func (s *stubModelPolicyService) ApplyPlatformParserOverrides(_ context.Context, merged map[string]string) map[string]string {
	if s.policy == nil || s.policy.Mode != types.ModelPolicyModeEnforce || s.policy.ParserProfile == nil {
		return merged
	}
	result := make(map[string]string, len(merged)+len(s.policy.ParserProfile.Overrides))
	for key, value := range merged {
		result[key] = value
	}
	for key, value := range s.policy.ParserProfile.Overrides {
		result[key] = value
	}
	return result
}

func (s *stubModelPolicyService) ValidateTenantParserConfig(context.Context, *types.ParserEngineConfig) error {
	return nil
}

// stubDocumentReader is a DocumentReader stand-in for transport-level API tests.
type stubDocumentReader struct{}

var _ interfaces.DocumentReader = (*stubDocumentReader)(nil)

func (s *stubDocumentReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return nil, nil
}

func (s *stubDocumentReader) Reconnect(string) error { return nil }

func (s *stubDocumentReader) IsConnected() bool { return true }

func (s *stubDocumentReader) ListEngines(context.Context, map[string]string) ([]types.ParserEngineInfo, error) {
	return nil, nil
}

// platformEnforceMinerUPolicy returns the workspace_provisioning-equivalent
// governance policy: enforce mode with a deployment-owned parser profile
// that supplies mineru_endpoint.
func platformEnforceMinerUPolicy() *stubModelPolicyService {
	return &stubModelPolicyService{policy: &types.ModelGovernancePolicy{
		Mode: types.ModelPolicyModeEnforce,
		ParserProfile: &types.ParserProfile{
			ID:        "deploy-locked-parser",
			Engine:    "mineru",
			FileTypes: []string{"pdf"},
			Overrides: map[string]string{
				"mineru_endpoint": ssrfBlockedMinerUEndpoint,
			},
		},
	}}
}

type parserEnginesAPIResponse struct {
	Code      int                      `json:"code"`
	Msg       string                   `json:"msg"`
	Data      []types.ParserEngineInfo `json:"data"`
	Connected bool                     `json:"connected"`
}

func decodeParserEnginesResponse(t *testing.T, recorder *httptest.ResponseRecorder) parserEnginesAPIResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, recorder.Code, "unexpected status; body=%s", recorder.Body.String())
	var resp parserEnginesAPIResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code, "API reported failure: %s", resp.Msg)
	return resp
}

func requireEngine(t *testing.T, engines []types.ParserEngineInfo, name string) types.ParserEngineInfo {
	t.Helper()
	for _, engine := range engines {
		if engine.Name == name {
			return engine
		}
	}
	t.Fatalf("parser engine %q not found in %+v", name, engines)
	return types.ParserEngineInfo{}
}

// requireMinerUPlatformOverrideApplied asserts that the platform-supplied
// mineru_endpoint reached the availability check: the reason must no longer
// be the "no endpoint configured" message, and the probe must have been
// blocked by SSRF (proving the endpoint value was actually consulted).
func requireMinerUPlatformOverrideApplied(t *testing.T, engines []types.ParserEngineInfo) {
	t.Helper()
	mineru := requireEngine(t, engines, "mineru")
	assert.NotEqual(t, "MinerU service not configured", mineru.UnavailableReason,
		"mineru must not report unconfigured when the platform profile supplies mineru_endpoint")
	assert.Contains(t, mineru.UnavailableReason, "SSRF",
		"mineru should have probed the platform-supplied endpoint and been rejected by the SSRF guard")
}

func TestListParserEnginesAppliesWorkspaceProvisioningParserOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{modelPolicy: platformEnforceMinerUPolicy()}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/parser-engines", nil)
	ctx.Set(types.TenantInfoContextKey.String(), &types.Tenant{ID: 1})

	handler.ListParserEngines(ctx)

	resp := decodeParserEnginesResponse(t, recorder)
	requireMinerUPlatformOverrideApplied(t, resp.Data)
}

func TestListParserEnginesWithoutPlatformProfileReportsMineruUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/parser-engines", nil)
	ctx.Set(types.TenantInfoContextKey.String(), &types.Tenant{ID: 1})

	handler.ListParserEngines(ctx)

	resp := decodeParserEnginesResponse(t, recorder)
	mineru := requireEngine(t, resp.Data, "mineru")
	assert.Equal(t, "MinerU service not configured", mineru.UnavailableReason)
}

func TestCheckParserEnginesAppliesWorkspaceProvisioningParserOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &SystemHandler{modelPolicy: platformEnforceMinerUPolicy()}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost, "/api/v1/system/parser-engines/check", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set(types.TenantInfoContextKey.String(), &types.Tenant{ID: 1})

	handler.CheckParserEngines(ctx)

	resp := decodeParserEnginesResponse(t, recorder)
	requireMinerUPlatformOverrideApplied(t, resp.Data)
}

func TestReconnectDocReaderAppliesWorkspaceProvisioningParserOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// ReconnectDocReader SSRF-validates its addr; whitelist the fake host so
	// the test never touches DNS and reset the singleton so the whitelist
	// changes do not leak into other tests.
	t.Setenv("SSRF_WHITELIST_EXTRA", "docreader.invalid")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	handler := &SystemHandler{
		documentReader: &stubDocumentReader{},
		modelPolicy:    platformEnforceMinerUPolicy(),
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost, "/api/v1/system/docreader/reconnect", strings.NewReader(`{"addr":"docreader.invalid:50051"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set(types.TenantInfoContextKey.String(), &types.Tenant{ID: 1})

	handler.ReconnectDocReader(ctx)

	resp := decodeParserEnginesResponse(t, recorder)
	requireMinerUPlatformOverrideApplied(t, resp.Data)
}
