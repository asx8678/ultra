package agent

import (
	"testing"

	"github.com/asx8678/ultra/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCoderPromptGuidesFabricSelection(t *testing.T) {
	t.Parallel()
	coder := config.Agent{AllowedTools: []string{"view", "fabric_exec"}}
	enabled := addFabricCodeModeGuidance("base", coder, false)
	require.Contains(t, enabled, "<fabric_code_mode>")
	require.Contains(t, enabled, "default execution path for complex coding work")
	require.Contains(t, enabled, "two or more tool calls")
	require.Contains(t, enabled, "one genuinely trivial operation")
	require.Contains(t, enabled, "top-level coder")

	require.Equal(t, "base", addFabricCodeModeGuidance("base", config.Agent{AllowedTools: []string{"view"}}, false))
	require.Equal(t, "base", addFabricCodeModeGuidance("base", coder, true))
}
