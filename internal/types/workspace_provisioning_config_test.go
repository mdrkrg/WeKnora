package types

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeProvisioningConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace_provisioning.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadWorkspaceProvisioningConfigRejectsDefaultsOverrides(t *testing.T) {
	content := `
version: 1
enabled: true
providers:
  - id: p
    adapter: generic
defaults:
  chat_model_id: builtin-chat
  embedding_model_id: builtin-embedding
  rerank_model_id: builtin-rerank
  parser_profile:
    id: d
    engine: mineru
    file_types: [pdf]
    overrides:
      mineru_endpoint: https://mineru.invalid
`
	t.Setenv(WorkspaceProvisioningConfigEnv, writeProvisioningConfig(t, content))
	_, err := LoadWorkspaceProvisioningConfig("")
	require.ErrorContains(t, err, "policy.parser_profile")
}

func TestLoadWorkspaceProvisioningConfigAcceptsPolicyOverrides(t *testing.T) {
	content := `
version: 1
enabled: true
providers:
  - id: p
    adapter: generic
defaults:
  chat_model_id: builtin-chat
  embedding_model_id: builtin-embedding
  rerank_model_id: builtin-rerank
  parser_profile:
    id: d
    engine: mineru
    file_types: [pdf]
policy:
  mode: enforce
  parser_profile:
    id: locked
    engine: mineru
    file_types: [pdf]
    overrides:
      mineru_endpoint: ${MINERU_ENDPOINT}
`
	t.Setenv(WorkspaceProvisioningConfigEnv, writeProvisioningConfig(t, content))
	t.Setenv("MINERU_ENDPOINT", "https://mineru.platform.invalid")
	cfg, err := LoadWorkspaceProvisioningConfig("")
	require.NoError(t, err)
	require.NotNil(t, cfg.Policy.ParserProfile)
	require.Equal(t, "https://mineru.platform.invalid", cfg.Policy.ParserProfile.Overrides["mineru_endpoint"])
}

func TestLoadWorkspaceProvisioningConfigLowercasesOverrideKeys(t *testing.T) {
	content := `
version: 1
enabled: true
providers:
  - id: p
    adapter: generic
defaults:
  chat_model_id: builtin-chat
  embedding_model_id: builtin-embedding
  rerank_model_id: builtin-rerank
  parser_profile:
    id: d
    engine: mineru
    file_types: [pdf]
policy:
  mode: enforce
  parser_profile:
    id: locked
    engine: mineru
    file_types: [pdf]
    overrides:
      MINERU_ENDPOINT: https://mineru.platform.invalid
`
	t.Setenv(WorkspaceProvisioningConfigEnv, writeProvisioningConfig(t, content))
	cfg, err := LoadWorkspaceProvisioningConfig("")
	require.NoError(t, err)
	// Keys are lowercased so lock checks and injection agree with the
	// lowercase keys the parser engines actually consume.
	_, exists := cfg.Policy.ParserProfile.Overrides["MINERU_ENDPOINT"]
	require.False(t, exists)
	require.Equal(t, "https://mineru.platform.invalid", cfg.Policy.ParserProfile.Overrides["mineru_endpoint"])
}
