package fabric

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
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
	Bindings         []CapabilityBinding
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

// ExecutionActivityKind identifies a best-effort live execution update.
type ExecutionActivityKind string

const (
	ActivityPhase         ExecutionActivityKind = "phase"
	ActivityCallStarted   ExecutionActivityKind = "call.started"
	ActivityCallCompleted ExecutionActivityKind = "call.completed"
)

// ExecutionActivity is a bounded presentation update. The terminal execution
// trace remains authoritative if an activity update is dropped.
type ExecutionActivity struct {
	Kind             ExecutionActivityKind `json:"kind"`
	ExecutionID      string                `json:"execution_id"`
	ParentToolCallID string                `json:"parent_tool_call_id"`
	SessionID        string                `json:"session_id,omitempty"`
	Sequence         uint64                `json:"sequence,omitempty"`
	Phase            string                `json:"phase,omitempty"`
	Ref              string                `json:"ref,omitempty"`
	CapabilityViewID string                `json:"capability_view_id,omitempty"`
	CapabilityCount  int                   `json:"capability_count,omitempty"`
	Providers        []string              `json:"providers,omitempty"`
	Outcome          ExecutionOutcome      `json:"outcome,omitempty"`
	FailureStage     FailureStage          `json:"failure_stage,omitempty"`
	Error            string                `json:"error,omitempty"`
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

const (
	// MaxSourceBytes bounds one model-generated Fabric program.
	MaxSourceBytes = 1 << 20
	// MaxExecutionTimeout prevents model-selected executions from running indefinitely.
	MaxExecutionTimeout = 5 * time.Minute
	// MaxMemoryBytes is the largest sandbox memory budget a caller may request.
	MaxMemoryBytes = 256 << 20
	// MaxAgentBudget bounds nested agent launches requested by one execution.
	MaxAgentBudget = 16
)

// ExecutionService runs checked source against one pinned registry view.
type ExecutionService struct {
	Registry   *Registry
	Compiler   ProgramCompiler
	Sandbox    SandboxRuntime
	Authorizer Authorizer
	Approvals  ApprovalController
	Budgets    BudgetLedger
	Limits     JSONLimits
	// Activity receives lossy, presentation-only execution updates. Callers
	// must use FabricExecResult.Trace as the authoritative record.
	Activity func(ExecutionActivity)
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
	if err := s.validateExecutionRequest(request); err != nil {
		result.Error = err.Error()
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
	compileActivity := capabilityActivity(view)
	compileActivity.Kind = ActivityPhase
	compileActivity.Phase = "compile"
	s.publishActivity(outer, compileActivity)
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
		result.Error = "Fabric program did not pass compilation checks"
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
		budgets: newExecutionBudget(request.AgentBudget, s.Budgets),
	}
	trace.Phase("execute")
	s.publishActivity(outer, ExecutionActivity{Kind: ActivityPhase, Phase: "execute"})
	sandboxResult, sandboxErr := s.Sandbox.Execute(executionCtx, SandboxExecutionRequest{
		JavaScript:       compiled.JavaScript,
		SourceMap:        compiled.SourceMap,
		SourceName:       fmt.Sprintf("fabric://%s/program.js", outer.ExecutionID),
		Strings:          cloneStrings(request.Strings),
		Timeout:          timeout,
		MemoryLimitBytes: request.MemoryLimitBytes,
		MaxLogBytes:      64 << 10,
		Bindings:         view.Bindings(),
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

func capabilityActivity(view *CapabilityView) ExecutionActivity {
	if view == nil {
		return ExecutionActivity{}
	}
	bindings := view.Bindings()
	providerSet := make(map[string]struct{})
	for _, binding := range bindings {
		if binding.Provider != "" {
			providerSet[binding.Provider] = struct{}{}
		}
	}
	providers := make([]string, 0, len(providerSet))
	for provider := range providerSet {
		providers = append(providers, provider)
	}
	slices.Sort(providers)
	return ExecutionActivity{
		CapabilityViewID: view.ID(),
		CapabilityCount:  len(bindings),
		Providers:        providers,
	}
}

func (s *ExecutionService) publishActivity(outer OuterInvocationContext, activity ExecutionActivity) {
	if s == nil || s.Activity == nil {
		return
	}
	activity.ExecutionID = outer.ExecutionID
	activity.ParentToolCallID = outer.ParentToolCallID
	activity.SessionID = outer.SessionID
	s.Activity(activity)
}

func (s *ExecutionService) validateExecutionRequest(request FabricExecRequest) error {
	if len(request.Code) > MaxSourceBytes {
		return fmt.Errorf("fabric source exceeds %d bytes", MaxSourceBytes)
	}
	if request.Timeout < 0 || request.Timeout > MaxExecutionTimeout {
		return fmt.Errorf("fabric timeout must be between 0 and %s", MaxExecutionTimeout)
	}
	if request.MemoryLimitBytes < 0 || request.MemoryLimitBytes > MaxMemoryBytes {
		return fmt.Errorf("fabric memory limit must be between 0 and %d bytes", MaxMemoryBytes)
	}
	if request.AgentBudget < 0 || request.AgentBudget > MaxAgentBudget {
		return fmt.Errorf("fabric agent budget must be between 0 and %d", MaxAgentBudget)
	}
	limits := normalizeJSONLimits(s.Limits)
	if request.ResultMaxBytes > limits.MaxBytes {
		return fmt.Errorf("fabric result limit exceeds %d bytes", limits.MaxBytes)
	}
	if err := ValidateJSON(request.Strings, limits); err != nil {
		return fmt.Errorf("fabric named strings: %w", err)
	}
	return nil
}

func (s *ExecutionService) acquireExecutionView(id string) (*CapabilityView, error) {
	if id != "" {
		return s.Registry.AcquireView(id)
	}
	return s.Registry.AcquireLiveView()
}

type executionBridge struct {
	service  *ExecutionService
	view     *CapabilityView
	outer    OuterInvocationContext
	trace    *TraceRecorder
	budgets  BudgetLedger
	nextCall atomic.Uint64
}

func (b *executionBridge) Call(ctx context.Context, ref string, args JSONObject) (JSONValue, error) {
	sequence := b.nextCall.Add(1)
	nestedToolCallID := fmt.Sprintf("%s:%d", b.outer.ParentToolCallID, sequence)
	activityRef, _ := projectTraceString(ref)
	b.service.publishActivity(b.outer, ExecutionActivity{
		Kind: ActivityCallStarted, Sequence: sequence, Ref: activityRef,
	})
	value, err := b.service.Registry.Invoke(ctx, InvokeRequest{
		View: b.view,
		Ref:  ref,
		Args: args,
		Invocation: InvocationContext{
			Context:          ctx,
			CWD:              b.outer.CWD,
			ExecutionID:      b.outer.ExecutionID,
			ParentToolCallID: b.outer.ParentToolCallID,
			NestedToolCallID: nestedToolCallID,
			SessionID:        b.outer.SessionID,
			CapabilityViewID: b.view.ID(),
		},
		Authorizer: b.service.Authorizer,
		Approvals:  b.service.Approvals,
		Budgets:    b.budgets,
		Trace:      b.trace,
		Limits:     b.service.Limits,
	})
	activity := ExecutionActivity{
		Kind: ActivityCallCompleted, Sequence: sequence, Ref: activityRef,
		Outcome: OutcomeSucceeded,
	}
	if err != nil {
		activity.Outcome = executionErrorOutcome(err)
		activity.Error, _ = projectTraceString(err.Error())
		var invocationErr *InvocationError
		if errors.As(err, &invocationErr) {
			activity.FailureStage = invocationErr.Stage
		}
	}
	b.service.publishActivity(b.outer, activity)
	return value, err
}

func executionErrorOutcome(err error) ExecutionOutcome {
	switch {
	case errors.Is(err, context.Canceled):
		return OutcomeAborted
	case errors.Is(err, context.DeadlineExceeded):
		return OutcomeTimedOut
	default:
		return OutcomeFailed
	}
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
