package chat

import (
	"testing"

	"github.com/asx8678/ultra/internal/message"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestToolStatusIconsAreColorIndependent(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	cases := []struct {
		name   string
		status ToolStatus
		want   string
	}{
		{name: "running", status: ToolStatusRunning, want: styles.ToolPending},
		{name: "success", status: ToolStatusSuccess, want: styles.ToolSuccess},
		{name: "error", status: ToolStatusError, want: styles.ToolError},
		{name: "canceled", status: ToolStatusCanceled, want: styles.ToolCancelled},
	}

	seen := make(map[string]struct{}, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ansi.Strip(toolIcon(&sty, tc.status))
			require.Equal(t, tc.want, got)
		})
		seen[tc.want] = struct{}{}
	}
	require.Len(t, seen, len(cases), "every status must have a distinct glyph")
}

func TestCanceledGenericToolShowsGlyphAndLabel(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	item := NewGenericToolMessageItem(
		&sty,
		message.ToolCall{ID: "tool-1", Name: "custom_tool", Input: `{}`},
		nil,
		true,
	)

	plain := ansi.Strip(item.Render(80))
	require.Contains(t, plain, styles.ToolCancelled)
	require.Contains(t, plain, "Canceled.")
	require.NotContains(t, plain, styles.ToolPending)
}
