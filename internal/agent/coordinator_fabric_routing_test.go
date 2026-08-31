package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

type routingTestParams struct{}

func TestModelFacingToolsForcesFabricWhenEnabled(t *testing.T) {
	t.Parallel()

	nativeTools := make([]fantasy.AgentTool, 2)
	fabricTools := make([]fantasy.AgentTool, 1)

	forced := modelFacingTools(nativeTools, fabricTools, true)
	require.Len(t, forced, 1)
	require.Same(t, &fabricTools[0], &forced[0])

	direct := modelFacingTools(nativeTools, fabricTools, false)
	require.Len(t, direct, 2)
	require.Same(t, &nativeTools[0], &direct[0])
}

func TestModelFacingToolsKeepsNativeGoAgentWithFabric(t *testing.T) {
	t.Parallel()

	agentTool := fantasy.NewAgentTool(AgentToolName, "Native Go agents", func(
		context.Context,
		routingTestParams,
		fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	})
	fabricTool := fantasy.NewAgentTool("fabric_exec", "Fabric", func(
		context.Context,
		routingTestParams,
		fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	})

	tools := modelFacingTools([]fantasy.AgentTool{agentTool}, []fantasy.AgentTool{fabricTool}, true)
	require.Len(t, tools, 2)
	require.Equal(t, "fabric_exec", tools[0].Info().Name)
	require.Equal(t, AgentToolName, tools[1].Info().Name)
}
