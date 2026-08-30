package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeProfiles(t *testing.T) {
	t.Parallel()

	interactive := InteractiveProfile()
	require.True(t, interactive.initializeAgent)
	require.True(t, interactive.interactive)
	require.True(t, interactive.clipboard)
	require.True(t, interactive.eventBridge)

	code := CodeProfile()
	require.True(t, code.initializeAgent)
	require.False(t, code.interactive)
	require.False(t, code.clipboard)
	require.False(t, code.eventBridge)
	require.True(t, code.mcp)
	require.True(t, code.trackLSP)

	server := ServerProfile()
	require.False(t, server.initializeAgent)
	require.True(t, server.eventBridge)
}
