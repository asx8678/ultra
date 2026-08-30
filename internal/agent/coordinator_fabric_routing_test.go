package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

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
