package agent

import (
	"context"
	_ "embed"
	"errors"

	"charm.land/fantasy"

	"github.com/asx8678/ultra/internal/agent/prompt"
	"github.com/asx8678/ultra/internal/config"
)

//go:embed templates/agent_tool.md
var agentToolDescription string

const AgentToolName = "agent"

func (c *coordinator) agentTool(ctx context.Context) (fantasy.AgentTool, error) {
	agentCfg, ok := c.cfg.Config().Agents[config.AgentTask]
	if !ok {
		return nil, errors.New("task agent not configured")
	}
	promptTemplate, err := taskPrompt(prompt.WithWorkingDir(c.cfg.WorkingDir()))
	if err != nil {
		return nil, err
	}

	taskAgent, err := c.buildAgent(ctx, promptTemplate, agentCfg, true)
	if err != nil {
		return nil, err
	}
	return c.newAgentTool(taskAgent), nil
}

func (c *coordinator) newAgentTool(taskAgent SessionAgent) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		AgentToolName,
		agentToolDescription,
		func(ctx context.Context, params AgentParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return c.handleAgentTool(ctx, taskAgent, params, call)
		},
	)
}
