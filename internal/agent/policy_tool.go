package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/permission"
)

type policyTool struct {
	inner       fantasy.AgentTool
	permissions permission.Service
}

func wrapToolsWithPolicy(agentTools []fantasy.AgentTool, permissions permission.Service) []fantasy.AgentTool {
	result := make([]fantasy.AgentTool, len(agentTools))
	for i, tool := range agentTools {
		result[i] = &policyTool{inner: tool, permissions: permissions}
	}
	return result
}

func (t *policyTool) Info() fantasy.ToolInfo {
	return t.inner.Info()
}

func (t *policyTool) ProviderOptions() fantasy.ProviderOptions {
	return t.inner.ProviderOptions()
}

func (t *policyTool) SetProviderOptions(options fantasy.ProviderOptions) {
	t.inner.SetProviderOptions(options)
}

func (t *policyTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	sessionID := tools.GetSessionFromContext(ctx)
	mode, scoped := permission.SessionMode(t.permissions, sessionID)
	if !scoped || mode == permission.ModeYolo || mode == permission.ModeAsk {
		return t.inner.Run(ctx, call)
	}

	if permission.ToolAllowed(mode, call.Name) {
		return t.inner.Run(ctx, call)
	}

	return fantasy.NewTextErrorResponse(fmt.Sprintf("tool %q is blocked by %s permission mode", call.Name, mode)), nil
}
