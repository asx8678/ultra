package agent

import (
	"testing"

	"github.com/asx8678/ultra/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNativePromptGuidesRepositoryGraphWorkflow(t *testing.T) {
	t.Parallel()
	agent := config.Agent{AllowedTools: []string{
		"repo_sketch", "repo_focus", "repo_dwell", "repo_impact",
	}}
	enabled := addFabricCodeModeGuidance("base", agent, false)
	require.Contains(t, enabled, "<repository_graph>")
	require.Contains(t, enabled, "call repo_sketch")
	require.Contains(t, enabled, "repo_dwell sequentially")
	require.Contains(t, enabled, "repo_impact before broad refactors")
	require.NotContains(t, enabled, "<fabric_code_mode>")

	partial := config.Agent{AllowedTools: []string{"repo_focus", "repo_dwell"}}
	require.Equal(t, "base", addFabricCodeModeGuidance("base", partial, false))
}

func TestCoderPromptGuidesFabricSelection(t *testing.T) {
	t.Parallel()
	coder := config.Agent{AllowedTools: []string{"view", "fabric_exec"}}
	enabled := addFabricCodeModeGuidance("base", coder, false)
	require.Contains(t, enabled, "<fabric_code_mode>")
	require.Contains(t, enabled, "execution path for ordinary host capabilities")
	require.Contains(t, enabled, "call fabric_exec")
	require.Contains(t, enabled, "agent tool remains directly available")
	require.Contains(t, enabled, "do not wrap agent orchestration in TypeScript")
	require.Contains(t, enabled, "one genuinely trivial operation")
	require.Contains(t, enabled, "top-level coder")
	require.Contains(t, enabled, "host.repo_sketch")
	require.Contains(t, enabled, "host.repo_focus")
	require.Contains(t, enabled, "host.repo_dwell only after focus")
	require.Contains(t, enabled, "host.repo_impact")

	require.Equal(t, "base", addFabricCodeModeGuidance("base", config.Agent{AllowedTools: []string{"view"}}, false))
	require.Equal(t, "base", addFabricCodeModeGuidance("base", coder, true))
}
