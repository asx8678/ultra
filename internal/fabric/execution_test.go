package fabric

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type compilerFunc func(context.Context, CompileRequest) (CompileResult, error)

func (f compilerFunc) Compile(ctx context.Context, request CompileRequest) (CompileResult, error) {
	return f(ctx, request)
}

type sandboxFunc func(context.Context, SandboxExecutionRequest) (SandboxExecutionResult, error)

func (f sandboxFunc) Execute(ctx context.Context, request SandboxExecutionRequest) (SandboxExecutionResult, error) {
	return f(ctx, request)
}

func TestFabricExecTypeFailureHasNoEffects(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	provider := &pipelineProvider{
		invoke: func(context.Context, JSONObject) (JSONValue, error) {
			t.Fatal("provider must not run after compiler diagnostics")
			return nil, nil
		},
	}
	provider.name = "host"
	provider.actions = []ActionDescriptor{pipelineDescriptor()}
	lease, err := registry.RegisterProvider(t.Context(), provider, RegisterOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Dispose()) })

	var sandboxCalls atomic.Int32
	service := &ExecutionService{
		Registry: registry,
		Compiler: compilerFunc(func(_ context.Context, request CompileRequest) (CompileResult, error) {
			require.Contains(t, request.Declarations, "declare const host")
			return CompileResult{Diagnostics: []ProgramDiagnostic{{
				Code:     "2304",
				Category: "name",
				Message:  "Cannot find name 'missing'",
				Line:     1,
				Column:   1,
			}}}, nil
		}),
		Sandbox: sandboxFunc(func(context.Context, SandboxExecutionRequest) (SandboxExecutionResult, error) {
			sandboxCalls.Add(1)
			return SandboxExecutionResult{}, nil
		}),
	}
	result := service.Execute(t.Context(), FabricExecRequest{Code: "return missing()"}, OuterInvocationContext{
		ExecutionID:      "execution-1",
		ParentToolCallID: "tool-1",
		CWD:              t.TempDir(),
	})
	require.Equal(t, OutcomeFailed, result.Outcome)
	require.Contains(t, result.Error, "compilation checks")
	require.Len(t, result.Diagnostics, 1)
	require.Zero(t, sandboxCalls.Load())
	require.Empty(t, result.Trace.Operations)
	require.Equal(t, []string{"compile"}, result.Trace.Phases)
}

func TestFabricExecRejectsUnsafeOuterLimitsBeforeCompile(t *testing.T) {
	t.Parallel()
	var compilerCalls atomic.Int32
	service := &ExecutionService{
		Registry: NewRegistry(),
		Compiler: compilerFunc(func(context.Context, CompileRequest) (CompileResult, error) {
			compilerCalls.Add(1)
			return CompileResult{}, nil
		}),
		Sandbox: sandboxFunc(func(context.Context, SandboxExecutionRequest) (SandboxExecutionResult, error) {
			t.Fatal("sandbox must not run for invalid outer limits")
			return SandboxExecutionResult{}, nil
		}),
	}
	outer := OuterInvocationContext{ExecutionID: "execution-limits"}
	for _, request := range []FabricExecRequest{
		{Code: strings.Repeat("x", MaxSourceBytes+1)},
		{Code: "return true", Timeout: MaxExecutionTimeout + 1},
		{Code: "return true", Timeout: -1},
		{Code: "return true", MemoryLimitBytes: MaxMemoryBytes + 1},
		{Code: "return true", MemoryLimitBytes: -1},
		{Code: "return true", AgentBudget: MaxAgentBudget + 1},
		{Code: "return true", AgentBudget: -1},
		{Code: "return true", ResultMaxBytes: DefaultJSONLimits().MaxBytes + 1},
	} {
		result := service.Execute(t.Context(), request, outer)
		require.Equal(t, OutcomeFailed, result.Outcome)
		require.NotEmpty(t, result.Error)
	}
	require.Zero(t, compilerCalls.Load())
}

func TestFabricExecReturnsBoundedJSON(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	value := JSONValue(strings.Repeat("x", 128))
	service := &ExecutionService{
		Registry: registry,
		Compiler: compilerFunc(func(_ context.Context, request CompileRequest) (CompileResult, error) {
			require.Equal(t, "return value", request.Source)
			return CompileResult{JavaScript: "value"}, nil
		}),
		Sandbox: sandboxFunc(func(_ context.Context, request SandboxExecutionRequest) (SandboxExecutionResult, error) {
			require.Equal(t, "value", request.JavaScript)
			return SandboxExecutionResult{Outcome: OutcomeSucceeded, Value: value, Logs: []string{}}, nil
		}),
	}
	outer := OuterInvocationContext{
		ExecutionID:      "execution-2",
		ParentToolCallID: "tool-2",
		CWD:              t.TempDir(),
	}
	result := service.Execute(t.Context(), FabricExecRequest{
		Code:           "return value",
		ResultMaxBytes: 32,
	}, outer)
	require.Equal(t, OutcomeFailed, result.Outcome)
	require.Nil(t, result.Value)
	require.Contains(t, result.Error, "result")
	require.ErrorIs(t, &InvocationError{Ref: "fabric_exec", Stage: FailureResult, Err: ErrJSONLimit}, ErrJSONLimit)
	require.Equal(t, []string{"compile", "execute"}, result.Trace.Phases)

	value = JSONObject{"ok": true}
	result = service.Execute(t.Context(), FabricExecRequest{
		Code:           "return value",
		ResultMaxBytes: 32,
	}, outer)
	require.Equal(t, OutcomeSucceeded, result.Outcome)
	require.Equal(t, JSONObject{"ok": true}, result.Value)
	require.Empty(t, result.Error)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.True(t, json.Valid(encoded))
}

func TestFabricExecPublishesLivePhaseAndCallActivity(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	provider := &pipelineProvider{}
	provider.name = "host"
	provider.actions = []ActionDescriptor{pipelineDescriptor()}
	lease, err := registry.RegisterProvider(t.Context(), provider, RegisterOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Dispose()) })

	var mu sync.Mutex
	var activities []ExecutionActivity
	service := &ExecutionService{
		Registry: registry,
		Compiler: compilerFunc(func(context.Context, CompileRequest) (CompileResult, error) {
			return CompileResult{JavaScript: "program"}, nil
		}),
		Sandbox: sandboxFunc(func(ctx context.Context, request SandboxExecutionRequest) (SandboxExecutionResult, error) {
			value, callErr := request.Bridge.Call(ctx, "host.run", JSONObject{
				"value": "ok", "prepared": true,
			})
			return SandboxExecutionResult{Outcome: OutcomeSucceeded, Value: value}, callErr
		}),
		Authorizer: authorizerFunc(func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			return AuthorizationDecision{Allowed: true}, nil
		}),
		Approvals: approvalFunc(func(context.Context, ApprovalRequest) (ApprovalDecision, error) {
			return ApprovalDecision{Kind: ApprovalAllow, Scope: ApprovalOnce}, nil
		}),
		Activity: func(activity ExecutionActivity) {
			mu.Lock()
			defer mu.Unlock()
			activities = append(activities, activity)
		},
	}
	result := service.Execute(t.Context(), FabricExecRequest{Code: "return host.run({})"}, OuterInvocationContext{
		ExecutionID: "execution-live", ParentToolCallID: "tool-live", SessionID: "session-live",
	})
	require.Equal(t, OutcomeSucceeded, result.Outcome, result.Error)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, activities, 4)
	require.Equal(t, ActivityPhase, activities[0].Kind)
	require.Equal(t, "compile", activities[0].Phase)
	require.NotEmpty(t, activities[0].CapabilityViewID)
	require.Equal(t, 1, activities[0].CapabilityCount)
	require.Equal(t, []string{"host"}, activities[0].Providers)
	require.Equal(t, ActivityPhase, activities[1].Kind)
	require.Equal(t, "execute", activities[1].Phase)
	require.Equal(t, ActivityCallStarted, activities[2].Kind)
	require.Equal(t, uint64(1), activities[2].Sequence)
	require.Equal(t, "host.run", activities[2].Ref)
	require.Equal(t, ActivityCallCompleted, activities[3].Kind)
	require.Equal(t, OutcomeSucceeded, activities[3].Outcome)
	for _, activity := range activities {
		require.Equal(t, "execution-live", activity.ExecutionID)
		require.Equal(t, "tool-live", activity.ParentToolCallID)
		require.Equal(t, "session-live", activity.SessionID)
	}
}
