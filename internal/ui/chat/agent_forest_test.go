package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/asx8678/ultra/internal/agent"
	"github.com/asx8678/ultra/internal/message"
	"github.com/asx8678/ultra/internal/ui/anim"
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
	require.Contains(t, plain, "Tools 1 · 1 running")
	require.NotContains(t, plain, "View")
	require.NotContains(t, plain, "full completion report")
	require.ElementsMatch(t,
		[]string{"running", "nested-view", "done", "failed", "canceled"},
		forest.AliasIDs(),
	)

	require.True(t, forest.ToggleExpanded())
	expanded := ansi.Strip(forest.Render(100))
	require.Contains(t, expanded, "View")
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

func TestAgentForestNarrowFallbackUsesIntactRows(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	forest := NewAgentForestMessageItem(&sty, "assistant-narrow-rows", []*AgentToolMessageItem{
		newAgentItem(t, &sty, "a", "Trace narrow output", false, nil),
		newAgentItem(t, &sty, "b", "Check connected rows", true, &message.ToolResult{ToolCallID: "b", Content: "done"}),
	})

	for _, width := range []int{12, 16, 20, 23} {
		rendered := forest.Render(width)
		plain := ansi.Strip(rendered)
		require.NotContains(t, plain, "╭", "width %d must not show a clipped card", width)
		require.NotContains(t, plain, "╯", "width %d must not show a clipped card", width)
		require.Contains(t, plain, "AGENTS")
		for line := range strings.SplitSeq(rendered, "\n") {
			require.LessOrEqualf(t, ansi.StringWidth(line), width, "width %d", width)
		}
	}
}

func TestAgentForestAdaptsSingletonAndLargeFanout(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	single := NewAgentForestMessageItem(&sty, "assistant-single", []*AgentToolMessageItem{
		newAgentItem(t, &sty, "single", "Inspect one task", false, nil),
	})
	singlePlain := ansi.Strip(single.Render(120))
	require.Equal(t, 4, 1+strings.Count(singlePlain, "\n"))
	require.Contains(t, singlePlain, "╭")
	require.Equal(t, 1, strings.Count(singlePlain, "RUNNING"))

	agents := make([]*AgentToolMessageItem, 0, 20)
	for i := 1; i <= 20; i++ {
		agents = append(agents, newAgentItem(
			t,
			&sty,
			fmt.Sprintf("agent-%02d", i),
			fmt.Sprintf("task %02d", i),
			false,
			nil,
		))
	}
	large := NewAgentForestMessageItem(&sty, "assistant-large", agents)
	largePlain := ansi.Strip(large.Render(120))
	require.LessOrEqual(t, 1+strings.Count(largePlain, "\n"), 21)
	require.NotContains(t, largePlain, "╭")
	require.Contains(t, largePlain, "A1")
	require.Contains(t, largePlain, "A20")
	require.Contains(t, largePlain, "task 01")
	require.Contains(t, largePlain, "task 20")
}

func TestAgentForestStaticRunningStateReusesCache(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	forest := NewAgentForestMessageItem(&sty, "assistant-static", []*AgentToolMessageItem{
		newAgentItem(t, &sty, "running", "Keep status visible", false, nil),
	})
	first := forest.RawRender(120)
	version := forest.Version()

	require.Nil(t, forest.StartAnimation())
	require.Nil(t, forest.Animate(anim.StepMsg{ID: "running"}))
	require.Equal(t, version, forest.Version())
	require.Equal(t, first, forest.RawRender(120))
}

func TestAgentForestCollapsedFailureIsSafeAndDiscoverable(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	failed := newAgentItem(t, &sty, "failed", "Inspect failure", true, &message.ToolResult{
		ToolCallID: "failed",
		Content:    "\x1b[2Jfailure reason",
		IsError:    true,
	})
	forest := NewAgentForestMessageItem(&sty, "assistant-failed", []*AgentToolMessageItem{
		newAgentItem(t, &sty, "running", "Keep working", false, nil),
		failed,
	})

	rendered := forest.Render(40)
	plain := ansi.Strip(rendered)
	require.Contains(t, plain, "AGENTS ▸ expand")
	require.Contains(t, plain, "Error: failure reason")
	require.NotContains(t, rendered, "\x1b[2J")

	require.True(t, forest.ToggleExpanded())
	require.Contains(t, ansi.Strip(forest.Render(40)), "AGENTS ▾ collapse")
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
