package fabric

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ProgramDiagnostic is a compact, model-repairable compiler error.
type ProgramDiagnostic struct {
	Code     string `json:"code,omitempty"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Length   int    `json:"length,omitempty"`
}

// CompileRequest pins source and declarations to one capability view.
type CompileRequest struct {
	Source       string
	SourceName   string
	Declarations string
	NamedStrings map[string]struct{}
}

// CompileResult is either executable JavaScript or diagnostics.
type CompileResult struct {
	JavaScript  string
	SourceMap   string
	Diagnostics []ProgramDiagnostic
}

// ProgramCompiler checks and transpiles guest source without executing it.
type ProgramCompiler interface {
	Compile(context.Context, CompileRequest) (CompileResult, error)
}

// SandboxBridge is the only authority exposed to guest code.
type SandboxBridge interface {
	Call(context.Context, string, JSONObject) (JSONValue, error)
	Progress(ActivityUpdate) error
}

// SandboxExecutionRequest contains one fresh guest execution.
type SandboxExecutionRequest struct {
	JavaScript       string
	SourceMap        string
	SourceName       string
	Strings          map[string]string
	Timeout          time.Duration
	MemoryLimitBytes int64
	MaxLogBytes      int
	Bridge           SandboxBridge
}

// SandboxExecutionResult is the terminal guest result.
type SandboxExecutionResult struct {
	Outcome ExecutionOutcome
	Value   JSONValue
	Error   string
	Logs    []string
}

// SandboxRuntime executes each request in a fresh isolated context.
type SandboxRuntime interface {
	Execute(context.Context, SandboxExecutionRequest) (SandboxExecutionResult, error)
}

// FabricExecRequest is the flat model-facing execution contract.
type FabricExecRequest struct {
	Code             string
	Strings          map[string]string
	Timeout          time.Duration
	MemoryLimitBytes int64
	TokenBudget      int64
	AgentBudget      int
	CapabilityViewID string
	IdempotencyKey   string
	ResultMaxBytes   int
	DisplayTitle     string
	DisplayCompact   bool
}

// OuterInvocationContext binds one Fabric execution to an Ultra tool call.
type OuterInvocationContext struct {
	ExecutionID      string
	ParentToolCallID string
	SessionID        string
	CWD              string
	HostID           string
}

// FabricExecResult is the bounded outer tool result.
type FabricExecResult struct {
	ExecutionID string              `json:"execution_id"`
	Outcome     ExecutionOutcome    `json:"outcome"`
	Value       JSONValue           `json:"value,omitempty"`
	Error       string              `json:"error,omitempty"`
	Diagnostics []ProgramDiagnostic `json:"diagnostics,omitempty"`
	Logs        []string            `json:"logs"`
	Trace       ExecutionTrace      `json:"trace"`
}

// ExecutionService runs checked source against one pinned registry view.
type ExecutionService struct {
	Registry   *Registry
	Compiler   ProgramCompiler
	Sandbox    SandboxRuntime
	Authorizer Authorizer
	Approvals  ApprovalController
	Budgets    BudgetLedger
	Limits     JSONLimits
}

// Execute runs one complete Fabric execution.
func (s *ExecutionService) Execute(
	ctx context.Context,
	request FabricExecRequest,
	outer OuterInvocationContext,
) FabricExecResult {
	trace := NewTraceRecorder()
	result := FabricExecResult{
		ExecutionID: outer.ExecutionID,
		Outcome:     OutcomeFailed,
		Logs:        []string{},
	}
	if s == nil || s.Registry == nil || s.Compiler == nil || s.Sandbox == nil {
		result.Error = "Fabric execution service is not fully configured"
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}

	view, err := s.acquireExecutionView(request.CapabilityViewID)
	if err != nil {
		result.Error = err.Error()
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}
	defer view.Release()

	trace.Phase("compile")
	declarations, err := s.Registry.Declarations(view)
	if err != nil {
		result.Error = err.Error()
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}
	namedStrings := make(map[string]struct{}, len(request.Strings))
	for name := range request.Strings {
		namedStrings[name] = struct{}{}
	}
	compiled, err := s.Compiler.Compile(ctx, CompileRequest{
		Source:       request.Code,
		SourceName:   fmt.Sprintf("fabric://%s/program.ts", outer.ExecutionID),
		Declarations: declarations,
		NamedStrings: namedStrings,
	})
	if err != nil {
		result.Error = fmt.Sprintf("compile Fabric program: %v", err)
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}
	if len(compiled.Diagnostics) > 0 {
		result.Diagnostics = append([]ProgramDiagnostic(nil), compiled.Diagnostics...)
		result.Error = "Fabric program did not pass type checking"
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}

	timeout := request.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	executionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bridge := &executionBridge{
		service: s,
		view:    view,
		outer:   outer,
		trace:   trace,
	}
	trace.Phase("execute")
	sandboxResult, sandboxErr := s.Sandbox.Execute(executionCtx, SandboxExecutionRequest{
		JavaScript:       compiled.JavaScript,
		SourceMap:        compiled.SourceMap,
		SourceName:       fmt.Sprintf("fabric://%s/program.js", outer.ExecutionID),
		Strings:          cloneStrings(request.Strings),
		Timeout:          timeout,
		MemoryLimitBytes: request.MemoryLimitBytes,
		MaxLogBytes:      64 << 10,
		Bridge:           bridge,
	})
	result.Logs = append([]string(nil), sandboxResult.Logs...)
	result.Outcome = sandboxResult.Outcome
	if sandboxErr != nil {
		result.Error = sandboxErr.Error()
		if errors.Is(sandboxErr, context.Canceled) {
			result.Outcome = OutcomeAborted
		} else if errors.Is(sandboxErr, context.DeadlineExceeded) {
			result.Outcome = OutcomeTimedOut
		} else if result.Outcome == "" {
			result.Outcome = OutcomeFailed
		}
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}
	if sandboxResult.Error != "" {
		result.Error = sandboxResult.Error
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}
	if sandboxResult.Outcome != OutcomeSucceeded {
		result.Error = "Fabric sandbox did not complete successfully"
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}

	limits := s.Limits
	if request.ResultMaxBytes > 0 {
		limits.MaxBytes = request.ResultMaxBytes
	}
	if err := ValidateJSON(sandboxResult.Value, limits); err != nil {
		result.Outcome = OutcomeFailed
		result.Error = (&InvocationError{Ref: "fabric_exec", Stage: FailureResult, Err: err}).Error()
		result.Trace = trace.Seal(result.Outcome, result.Error)
		return result
	}
	result.Value = cloneJSONValue(sandboxResult.Value)
	result.Trace = trace.Seal(result.Outcome, "")
	return result
}

func (s *ExecutionService) acquireExecutionView(id string) (*CapabilityView, error) {
	if id != "" {
		return s.Registry.AcquireView(id)
	}
	return s.Registry.AcquireLiveView()
}

type executionBridge struct {
	service *ExecutionService
	view    *CapabilityView
	outer   OuterInvocationContext
	trace   *TraceRecorder
}

func (b *executionBridge) Call(ctx context.Context, ref string, args JSONObject) (JSONValue, error) {
	return b.service.Registry.Invoke(ctx, InvokeRequest{
		View: b.view,
		Ref:  ref,
		Args: args,
		Invocation: InvocationContext{
			Context:          ctx,
			CWD:              b.outer.CWD,
			ExecutionID:      b.outer.ExecutionID,
			ParentToolCallID: b.outer.ParentToolCallID,
			SessionID:        b.outer.SessionID,
			CapabilityViewID: b.view.ID(),
		},
		Authorizer: b.service.Authorizer,
		Approvals:  b.service.Approvals,
		Budgets:    b.service.Budgets,
		Trace:      b.trace,
		Limits:     b.service.Limits,
	})
}

func (b *executionBridge) Progress(ActivityUpdate) error {
	return nil
}

func cloneStrings(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}
