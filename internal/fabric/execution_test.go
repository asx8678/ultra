package fabric

import (
	"context"
	"encoding/json"
	"strings"
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
