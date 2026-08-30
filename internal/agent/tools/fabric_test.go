package tools

import (
	"context"
	"sync/atomic"
	"testing"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/permission"
	"github.com/stretchr/testify/require"
)

type fabricTestParams struct {
	Path string `json:"path" description:"Test path"`
}

func fabricTestTool(name string, calls *atomic.Int32) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		name,
		name+" test tool",
		func(context.Context, fabricTestParams, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if calls != nil {
				calls.Add(1)
			}
			return fantasy.NewTextResponse(name + " complete"), nil
		},
	)
}

func TestUltraProviderCatalogMatchesTools(t *testing.T) {
	t.Parallel()
	agentTools := []fantasy.AgentTool{
		fabricTestTool("write", nil), fabricTestTool("view", nil), fabricTestTool("grep", nil),
	}
	catalog := NewCatalog(agentTools)
	provider, err := NewUltraFabricProvider(agentTools)
	require.NoError(t, err)
	descriptors, err := provider.List(t.Context(), fabric.ListActionsRequest{}, fabric.DiscoveryContext{})
	require.NoError(t, err)
	catalogNames := make([]string, 0, len(agentTools))
	for _, descriptor := range catalog.List() {
		catalogNames = append(catalogNames, descriptor.Name)
	}
	providerNames := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		providerNames = append(providerNames, descriptor.Name)
		require.NotEmpty(t, descriptor.InputSchema)
	}
	require.Equal(t, catalogNames, providerNames)
	require.Equal(t, []string{"grep", "view", "write"}, providerNames)
}

func TestFabricExecToolFlatSchema(t *testing.T) {
	t.Parallel()
	info := NewFabricExecTool(nil, "test-host", t.TempDir()).Info()
	require.Equal(t, FabricExecToolName, info.Name)
	require.Contains(t, info.Required, "code")
	for _, name := range []string{"code", "strings", "timeout_ms", "display"} {
		require.Contains(t, info.Parameters, name)
	}
	require.NotContains(t, info.Parameters, "request")
	for name, parameter := range info.Parameters {
		if name == "display" || name == "strings" {
			continue
		}
		property, ok := parameter.(map[string]any)
		require.True(t, ok, name)
		require.NotEqual(t, "object", property["type"], name)
		require.NotEqual(t, "array", property["type"], name)
	}
}

func TestUltraFabricAuthorizerFailsClosedWithoutPermissionService(t *testing.T) {
	t.Parallel()
	decision, err := (UltraFabricAuthorizer{}).Authorize(t.Context(), fabric.AuthorizationRequest{
		Ref: "host.view",
	})
	require.NoError(t, err)
	require.False(t, decision.Allowed)
	require.Contains(t, decision.Reason, "permission service")
}

func TestFabricExecToolRespectsPermissionPolicy(t *testing.T) {
	t.Parallel()
	var writeCalls atomic.Int32
	var viewCalls atomic.Int32
	provider, err := NewUltraFabricProvider([]fantasy.AgentTool{
		fabricTestTool("write", &writeCalls), fabricTestTool("view", &viewCalls),
	})
	require.NoError(t, err)
	registry := fabric.NewRegistry()
	lease, err := registry.RegisterProvider(t.Context(), provider, fabric.RegisterOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Dispose()) })
	view, err := registry.AcquireLiveView()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Release()) })
	permissions := permission.NewPermissionService(t.TempDir(), false, nil)
	permission.SetSessionMode(permissions, "session", permission.ModeReadOnly)
	authorizer := UltraFabricAuthorizer{Permissions: permissions}
	invoke := func(ref string) (fabric.JSONValue, error) {
		return registry.Invoke(t.Context(), fabric.InvokeRequest{
			View: view, Ref: ref, Args: fabric.JSONObject{"path": "file.txt"},
			Invocation: fabric.InvocationContext{
				SessionID: "session", ExecutionID: "execution", NestedToolCallID: "nested",
			},
			Authorizer: authorizer, Approvals: UltraFabricApprovals{},
		})
	}
	_, err = invoke("host.write")
	require.ErrorIs(t, err, fabric.ErrUnauthorized)
	require.Zero(t, writeCalls.Load())
	result, err := invoke("host.view")
	require.NoError(t, err)
	require.Equal(t, int32(1), viewCalls.Load())
	object, ok := result.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "view complete", object["content"])
}

type fabricExecutorFunc func(context.Context, fabric.FabricExecRequest, fabric.OuterInvocationContext) fabric.FabricExecResult

func (f fabricExecutorFunc) Execute(
	ctx context.Context,
	request fabric.FabricExecRequest,
	outer fabric.OuterInvocationContext,
) fabric.FabricExecResult {
	return f(ctx, request, outer)
}

func TestFabricExecToolCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), SessionIDContextKey, "session"))
	cancel()
	executor := fabricExecutorFunc(func(
		ctx context.Context,
		request fabric.FabricExecRequest,
		outer fabric.OuterInvocationContext,
	) fabric.FabricExecResult {
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		require.Equal(t, "return 1", request.Code)
		require.Equal(t, "call-1", outer.ExecutionID)
		require.Equal(t, "session", outer.SessionID)
		return fabric.FabricExecResult{
			ExecutionID: outer.ExecutionID, Outcome: fabric.OutcomeAborted,
			Error: context.Canceled.Error(), Logs: []string{},
			Trace: fabric.ExecutionTrace{Kind: "ultra.fabric.execution", Version: 1, Outcome: fabric.OutcomeAborted},
		}
	})
	response, err := NewFabricExecTool(executor, "test-host", t.TempDir()).Run(ctx, fantasy.ToolCall{
		ID: "call-1", Name: FabricExecToolName, Input: `{"code":"return 1"}`,
	})
	require.NoError(t, err)
	require.True(t, response.IsError)
	require.Contains(t, response.Content, context.Canceled.Error())
}
