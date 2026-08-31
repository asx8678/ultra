package chat

import (
	"encoding/json"
	"testing"

	"github.com/asx8678/ultra/internal/agent"
	"github.com/asx8678/ultra/internal/message"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func TestAgentToolChildSessionIDs(t *testing.T) {
	t.Parallel()
	createID := func(messageID, toolCallID string) string { return messageID + "$$" + toolCallID }
	sty := styles.CharmtonePantera()

	t.Run("legacy", func(t *testing.T) {
		input, err := json.Marshal(agent.AgentParams{Prompt: "find it"})
		require.NoError(t, err)
		item := NewAgentToolMessageItem(&sty, message.ToolCall{ID: "call-1", Input: string(input)}, nil, false)
		item.SetMessageID("message-1")
		require.Equal(t, []string{"message-1$$call-1"}, item.ChildSessionIDs(createID))
	})

	t.Run("parallel generated ids", func(t *testing.T) {
		input, err := json.Marshal(agent.AgentParams{Tasks: []agent.AgentTask{
			{ID: "review", Prompt: "review"}, {Prompt: "test"},
		}})
		require.NoError(t, err)
		item := NewAgentToolMessageItem(&sty, message.ToolCall{ID: "call-2", Input: string(input)}, nil, false)
		item.SetMessageID("message-2")
		require.Equal(t, []string{
			"message-2$$call-2-review", "message-2$$call-2-task-2",
		}, item.ChildSessionIDs(createID))
	})

	t.Run("council synthesis", func(t *testing.T) {
		input, err := json.Marshal(agent.AgentParams{Mode: "council", Tasks: []agent.AgentTask{
			{ID: "synthesis", Prompt: "member"},
		}})
		require.NoError(t, err)
		item := NewAgentToolMessageItem(&sty, message.ToolCall{ID: "call-3", Input: string(input)}, nil, false)
		item.SetMessageID("message-3")
		require.Equal(t, []string{
			"message-3$$call-3-synthesis", "message-3$$call-3-synthesis-2",
		}, item.ChildSessionIDs(createID))
	})

	t.Run("result is authoritative", func(t *testing.T) {
		input, err := json.Marshal(agent.AgentParams{Action: "wait", RunID: "run"})
		require.NoError(t, err)
		result, err := json.Marshal(agent.AgentRunSnapshot{Tasks: []agent.AgentTaskResult{
			{ID: "a", SessionID: "original$$call-a"}, {ID: "b", SessionID: "original$$call-b"},
		}})
		require.NoError(t, err)
		item := NewAgentToolMessageItem(&sty, message.ToolCall{ID: "wait-call", Input: string(input)}, &message.ToolResult{Content: string(result)}, false)
		item.SetMessageID("wait-message")
		require.Equal(t, []string{"original$$call-a", "original$$call-b"}, item.ChildSessionIDs(createID))
	})
}

func TestAgentPromptSummary(t *testing.T) {
	t.Parallel()
	require.Equal(t, "parallel · 2 tasks", agentPromptSummary(agent.AgentParams{
		Tasks: []agent.AgentTask{{Prompt: "one"}, {Prompt: "two"}},
	}))
	require.Equal(t, "wait · agent-123", agentPromptSummary(agent.AgentParams{
		Action: "wait", RunID: "agent-123",
	}))
}
