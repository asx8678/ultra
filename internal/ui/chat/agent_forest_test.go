package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/asx8678/ultra/internal/agent"
	"github.com/asx8678/ultra/internal/message"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestExtractMessageItemsGroupsConsecutiveAgentsWithoutReordering(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := &message.Message{
		ID:   "assistant-1",
		Role: message.Assistant,
		Parts: []message.ContentPart{
			agentToolCall(t, "agent-a", "Audit storage", false),
			agentToolCall(t, "agent-b", "Audit rendering", false),
			message.ToolCall{ID: "bash-1", Name: "bash", Input: `{"command":"go test ./..."}`},
			agentToolCall(t, "agent-c", "Audit tests", false),
		},
	}

	items := ExtractMessageItems(&sty, msg, nil, "")
	require.Len(t, items, 3)
	first, ok := items[0].(*AgentForestMessageItem)
	require.True(t, ok)
	require.Equal(t, AgentForestID(msg.ID, "agent-a"), first.ID())
	require.Equal(t, []string{"agent-a", "agent-b"}, first.AliasIDs())
	require.Equal(t, "bash-1", items[1].ID())
	last, ok := items[2].(*AgentForestMessageItem)
	require.True(t, ok)
	require.Equal(t, AgentForestID(msg.ID, "agent-c"), last.ID())
	require.Equal(t, []string{"agent-c"}, last.AliasIDs())
}

func TestAgentForestRendersStatusesNestedActivityAndExpansion(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	running := newAgentItem(t, &sty, "running", "Trace events", false, nil)
	done := newAgentItem(t, &sty, "done", "Inspect renderer", true, &message.ToolResult{
		ToolCallID: "done", Content: "full completion report",
	})
	failed := newAgentItem(t, &sty, "failed", "Probe failures", true, &message.ToolResult{
		ToolCallID: "failed", Content: "failure details", IsError: true,
	})
	canceled := newAgentItem(t, &sty, "canceled", "Check cancellation", false, nil)
	canceled.SetStatus(ToolStatusCanceled)
	nested := NewToolMessageItem(&sty, "child-message", message.ToolCall{
		ID: "nested-view", Name: "view", Input: `{"file_path":"README.md"}`,
	}, nil, false, "")
	running.AddNestedTool(nested)

	forest := NewAgentForestMessageItem(&sty, "assistant-2", []*AgentToolMessageItem{
		running, done, failed, canceled,
	})
	plain := ansi.Strip(forest.Render(100))
	require.Contains(t, plain, "4 agents · 1 running · 1 done · 1 failed · 1 canceled")
	for _, status := range []string{"RUNNING", "DONE", "FAILED", "CANCELED"} {
		require.Contains(t, plain, status)
	}
	require.Contains(t, plain, "View")
	require.NotContains(t, plain, "full completion report")
	require.ElementsMatch(t,
		[]string{"running", "nested-view", "done", "failed", "canceled"},
		forest.AliasIDs(),
	)

	require.True(t, forest.ToggleExpanded())
	expanded := ansi.Strip(forest.Render(100))
	require.Contains(t, expanded, "full completion report")
	require.Contains(t, expanded, "failure details")
}

func TestAgentForestRenderingIsWidthBounded(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	forest := NewAgentForestMessageItem(&sty, "assistant-narrow", []*AgentToolMessageItem{
		newAgentItem(t, &sty, "a", strings.Repeat("wide task ", 20), false, nil),
		newAgentItem(t, &sty, "b", "short task", true, &message.ToolResult{ToolCallID: "b", Content: strings.Repeat("result ", 30)}),
	})
	forest.ToggleExpanded()

	for _, width := range []int{12, 20, 40, 80, 120} {
		for line := range strings.SplitSeq(forest.Render(width), "\n") {
			require.LessOrEqualf(t, ansi.StringWidth(line), width, "width %d", width)
		}
	}
}

func TestAgentForestAddAgentPreservesOrderAndIdentity(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	first := newAgentItem(t, &sty, "first", "First", false, nil)
	forest := NewAgentForestMessageItem(&sty, "assistant-live", []*AgentToolMessageItem{first})
	id := forest.ID()
	version := forest.Version()

	second := newAgentItem(t, &sty, "second", "Second", false, nil)
	forest.AddAgent(second)
	forest.AddAgent(second)

	require.Equal(t, id, forest.ID())
	require.Equal(t, []string{"first", "second"}, forest.AliasIDs())
	require.Len(t, forest.AgentTools(), 2)
	require.Greater(t, forest.Version(), version)
}

func agentToolCall(t *testing.T, id, prompt string, finished bool) message.ToolCall {
	t.Helper()
	input, err := json.Marshal(agent.AgentParams{Prompt: prompt})
	require.NoError(t, err)
	return message.ToolCall{ID: id, Name: agent.AgentToolName, Input: string(input), Finished: finished}
}

func newAgentItem(
	t *testing.T,
	sty *styles.Styles,
	id, prompt string,
	finished bool,
	result *message.ToolResult,
) *AgentToolMessageItem {
	t.Helper()
	return NewAgentToolMessageItem(sty, agentToolCall(t, id, prompt, finished), result, false)
}
