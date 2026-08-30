package agent

import (
	"context"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/hooks"
)

const ultraFabricHostID = "ultra"

type fabricRuntime interface {
	tools.FabricExecutor
	ReplaceNativeTools(context.Context, []fantasy.AgentTool, *hooks.Runner) error
	Close() error
}

func (c *coordinator) fabricExecTool(
	ctx context.Context,
	nativeTools []fantasy.AgentTool,
	hookRunner *hooks.Runner,
) (fantasy.AgentTool, error) {
	created := false
	if c.fabricRuntime == nil {
		runtime, err := newFabricRuntime(c.permissions, c.notify)
		if err != nil {
			return nil, err
		}
		c.fabricRuntime = runtime
		created = true
	}
	if err := c.fabricRuntime.ReplaceNativeTools(ctx, nativeTools, hookRunner); err != nil {
		if created {
			_ = c.fabricRuntime.Close()
			c.fabricRuntime = nil
		}
		return nil, err
	}
	return tools.NewFabricExecTool(c.fabricRuntime, ultraFabricHostID, c.cfg.WorkingDir()), nil
}
