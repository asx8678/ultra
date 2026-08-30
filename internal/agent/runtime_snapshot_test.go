package agent

import (
	"testing"

	"github.com/asx8678/ultra/internal/agent/tools/mcp"
	"github.com/asx8678/ultra/internal/config"
	"github.com/stretchr/testify/require"
)

func TestEnsureRuntimeSkipsUnchangedSnapshot(t *testing.T) {
	env := testEnv(t)
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)

	coord := &coordinator{
		cfg:                     cfg,
		runtimeConfigGeneration: cfg.RuntimeGeneration(),
		runtimeMCPGeneration:    mcp.ToolGeneration(),
	}

	// A rebuild would fail because this deliberately minimal coordinator has
	// no configured current agent. Matching generations must take the no-op
	// path instead.
	require.NoError(t, coord.ensureRuntime(t.Context()))
}
