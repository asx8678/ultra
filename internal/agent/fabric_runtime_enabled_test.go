//go:build !fabric_disabled

package agent

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/agent/notify"
	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/config"
	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/hooks"
	"github.com/asx8678/ultra/internal/permission"
	"github.com/asx8678/ultra/internal/pubsub"
	"github.com/stretchr/testify/require"
)

type fabricRuntimeViewParams struct {
	Path string `json:"path" description:"Path to view"`
}

type recordingFabricPublisher struct {
	mu     sync.Mutex
	values []notify.Notification
}

func (p *recordingFabricPublisher) Publish(_ pubsub.EventType, value notify.Notification) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.values = append(p.values, value)
}

func (p *recordingFabricPublisher) PublishMustDeliver(
	context.Context,
	pubsub.EventType,
	notify.Notification,
) {
}

func (p *recordingFabricPublisher) snapshot() []notify.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]notify.Notification(nil), p.values...)
}

func TestFabricRuntimeExecutesCapturedUltraTool(t *testing.T) {
	t.Parallel()
	permissions := permission.NewPermissionService(t.TempDir(), false, nil)
	permission.SetSessionMode(permissions, "session", permission.ModeReadOnly)
	publisher := &recordingFabricPublisher{}
	runtime, err := newFabricRuntime(permissions, publisher)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })

	var calls atomic.Int32
	var nestedToolCallID string
	view := fantasy.NewAgentTool("view", "View a file", func(
		_ context.Context,
		params fabricRuntimeViewParams,
		call fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		calls.Add(1)
		nestedToolCallID = call.ID
		return fantasy.NewTextResponse(params.Path), nil
	})
	require.NoError(t, runtime.ReplaceNativeTools(t.Context(), []fantasy.AgentTool{view}, nil))

	result := runtime.Execute(t.Context(), fabric.FabricExecRequest{
		Code:    `const result: unknown = await host.view({ path: π.path }); return result;`,
		Strings: map[string]string{"path": "README.md"},
	}, fabric.OuterInvocationContext{
		ExecutionID: "execution", ParentToolCallID: "outer", SessionID: "session", CWD: t.TempDir(),
	})
	require.Equal(t, fabric.OutcomeSucceeded, result.Outcome, result.Error)
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, "outer:1", nestedToolCallID)
	value, ok := result.Value.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "README.md", value["content"])
	require.Len(t, result.Trace.Operations, 1)
	require.Equal(t, "host.view", result.Trace.Operations[0].Ref)

	notifications := publisher.snapshot()
	require.Len(t, notifications, 4)
	for _, notification := range notifications {
		require.Equal(t, notify.TypeFabricActivity, notification.Type)
		require.NotNil(t, notification.FabricActivity)
		require.Equal(t, "outer", notification.FabricActivity.ParentToolCallID)
	}
	require.Equal(t, fabric.ActivityPhase, notifications[0].FabricActivity.Kind)
	require.Equal(t, fabric.ActivityCallCompleted, notifications[3].FabricActivity.Kind)
}

func TestFabricRuntimeHookAllowReusesNestedToolCallIDForPermission(t *testing.T) {
	t.Parallel()
	permissions := permission.NewPermissionService(t.TempDir(), false, nil)
	permission.SetSessionMode(permissions, "session", permission.ModeAsk)
	runtime, err := newFabricRuntime(permissions, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })

	var calls atomic.Int32
	write := fantasy.NewAgentTool("write", "Write a file", func(
		ctx context.Context,
		params fabricRuntimeViewParams,
		call fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		granted, err := permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID: "session", ToolCallID: call.ID, ToolName: call.Name,
			Action: "write", Path: params.Path,
		})
		if err != nil || !granted {
			return fantasy.ToolResponse{}, err
		}
		calls.Add(1)
		return fantasy.NewTextResponse("written"), nil
	})
	runner := hooks.NewRunner([]config.HookConfig{{
		Command: `echo '{"decision":"allow"}'`,
	}}, t.TempDir(), t.TempDir())
	require.NoError(t, runtime.ReplaceNativeTools(t.Context(), []fantasy.AgentTool{write}, runner))

	result := runtime.Execute(t.Context(), fabric.FabricExecRequest{
		Code: `return await host.write({ path: "allowed" });`,
	}, fabric.OuterInvocationContext{
		ExecutionID: "execution", ParentToolCallID: "outer", SessionID: "session", CWD: t.TempDir(),
	})
	require.Equal(t, fabric.OutcomeSucceeded, result.Outcome, result.Error)
	require.Equal(t, int32(1), calls.Load())
}

func TestCoordinatorClosesFabricRuntimeWhenToolBecomesDisallowed(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	t.Cleanup(func() { require.NoError(t, coord.Close()) })

	require.NoError(t, coord.UpdateModels(t.Context()))
	require.NotNil(t, coord.fabricRuntime)

	agentCfg := coord.cfg.Config().Agents[config.AgentCoder]
	agentCfg.AllowedTools = slices.DeleteFunc(agentCfg.AllowedTools, func(name string) bool {
		return name == tools.FabricExecToolName
	})
	coord.cfg.Config().Agents[config.AgentCoder] = agentCfg

	require.NoError(t, coord.UpdateModels(t.Context()))
	require.Nil(t, coord.fabricRuntime)
}

func TestFabricRuntimeRejectsWriteInReadOnlySession(t *testing.T) {
	t.Parallel()
	permissions := permission.NewPermissionService(t.TempDir(), false, nil)
	permission.SetSessionMode(permissions, "session", permission.ModeReadOnly)
	runtime, err := newFabricRuntime(permissions, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close()) })

	write := fantasy.NewAgentTool("write", "Write a file", func(
		context.Context,
		fabricRuntimeViewParams,
		fantasy.ToolCall,
	) (fantasy.ToolResponse, error) {
		t.Fatal("write tool must not execute in read-only mode")
		return fantasy.ToolResponse{}, nil
	})
	require.NoError(t, runtime.ReplaceNativeTools(t.Context(), []fantasy.AgentTool{write}, nil))

	result := runtime.Execute(t.Context(), fabric.FabricExecRequest{
		Code: `return await host.write({ path: "blocked" });`,
	}, fabric.OuterInvocationContext{
		ExecutionID: "execution", ParentToolCallID: "outer", SessionID: "session", CWD: t.TempDir(),
	})
	require.Equal(t, fabric.OutcomeFailed, result.Outcome)
	require.Contains(t, result.Error, "unauthorized")
}
