package model

import (
	"testing"

	"github.com/asx8678/ultra/internal/config"
	"github.com/asx8678/ultra/internal/message"
	"github.com/asx8678/ultra/internal/ui/chat"
	"github.com/asx8678/ultra/internal/ui/common"
	"github.com/stretchr/testify/require"
)

func TestChatIndexesAgentForestMembersAndNestedTools(t *testing.T) {
	t.Parallel()

	com := common.DefaultCommon(nil)
	first := chat.NewAgentToolMessageItem(com.Styles, message.ToolCall{
		ID: "agent-a", Name: "agent", Input: `{"prompt":"A"}`,
	}, nil, false)
	second := chat.NewAgentToolMessageItem(com.Styles, message.ToolCall{
		ID: "agent-b", Name: "agent", Input: `{"prompt":"B"}`,
	}, nil, false)
	forest := chat.NewAgentForestMessageItem(com.Styles, "assistant", []*chat.AgentToolMessageItem{first, second})
	model := NewChat(com, config.ScrollbarDefault)
	model.SetMessages(forest)

	require.Same(t, forest, model.MessageItem(forest.ID()))
	require.Same(t, forest, model.MessageItem("agent-a"))
	require.Same(t, forest, model.MessageItem("agent-b"))

	nested := chat.NewToolMessageItem(com.Styles, "child", message.ToolCall{
		ID: "nested", Name: "view", Input: `{"file_path":"README.md"}`,
	}, nil, false, "")
	first.AddNestedTool(nested)
	forest.Touch()
	model.UpdateNestedToolIDs("agent-a")
	require.Same(t, forest, model.MessageItem("nested"))

	model.RemoveMessage(forest.ID())
	require.Nil(t, model.MessageItem(forest.ID()))
	require.Nil(t, model.MessageItem("agent-a"))
	require.Nil(t, model.MessageItem("agent-b"))
	require.Nil(t, model.MessageItem("nested"))
}
