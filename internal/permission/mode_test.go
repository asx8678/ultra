package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    Mode
		tool    string
		granted bool
		err     error
	}{
		{name: "read only grants view", mode: ModeReadOnly, tool: "view", granted: true},
		{name: "read only grants Fabric envelope", mode: ModeReadOnly, tool: "fabric_exec", granted: true},
		{name: "read only denies write", mode: ModeReadOnly, tool: "write"},
		{name: "read only denies network read", mode: ModeReadOnly, tool: "fetch"},
		{name: "read only denies shell", mode: ModeReadOnly, tool: "bash"},
		{name: "read only denies unknown", mode: ModeReadOnly, tool: "plugin_tool"},
		{name: "accept edits grants write", mode: ModeAcceptEdits, tool: "write", granted: true},
		{name: "accept edits grants lsp rename", mode: ModeAcceptEdits, tool: "lsp_rename", granted: true},
		{name: "accept edits grants session write", mode: ModeAcceptEdits, tool: "todos", granted: true},
		{name: "accept edits denies shell", mode: ModeAcceptEdits, tool: "bash"},
		{name: "accept edits denies network", mode: ModeAcceptEdits, tool: "fetch"},
		{name: "yolo grants unknown", mode: ModeYolo, tool: "plugin_tool", granted: true},
		{name: "ask fails without frontend", mode: ModeAsk, tool: "bash", err: ErrApprovalRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewPermissionService(t.TempDir(), false, nil)
			SetSessionMode(svc, "session", tt.mode)
			granted, err := svc.Request(context.Background(), CreatePermissionRequest{
				SessionID:  "session",
				ToolCallID: "call",
				ToolName:   tt.tool,
				Action:     "execute",
				Path:       ".",
			})
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.granted, granted)
		})
	}
}

func TestParseMode(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"ask", "read-only", "accept-edits", "yolo"} {
		mode, ok := ParseMode(value)
		require.True(t, ok)
		require.Equal(t, Mode(value), mode)
	}
	_, ok := ParseMode("unsafe")
	require.False(t, ok)
}
