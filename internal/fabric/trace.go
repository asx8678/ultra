package fabric

import (
	"slices"
	"sync"
)

// ExecutionOutcome is the terminal state of an execution or nested call.
type ExecutionOutcome string

const (
	OutcomeSucceeded     ExecutionOutcome = "succeeded"
	OutcomeFailed        ExecutionOutcome = "failed"
	OutcomeAborted       ExecutionOutcome = "aborted"
	OutcomeTimedOut      ExecutionOutcome = "timed_out"
	OutcomeIndeterminate ExecutionOutcome = "indeterminate"
)

// FailureStage locates a nested-call failure in the registry pipeline.
type FailureStage string

const (
	FailureResolve   FailureStage = "resolve"
	FailureAuthorize FailureStage = "authorize"
	FailurePrepare   FailureStage = "prepare"
	FailureValidate  FailureStage = "validate"
	FailureApprove   FailureStage = "approve"
	FailureEffect    FailureStage = "effect"
	FailureInvoke    FailureStage = "invoke"
	FailureResult    FailureStage = "result"
	FailureGuard     FailureStage = "guard"
	FailureRuntime   FailureStage = "runtime"
)

// TraceOperation is a stable, issue-ordered nested action record.
type TraceOperation struct {
	Type         string           `json:"type"`
	Sequence     int              `json:"sequence"`
	Ref          string           `json:"ref"`
	Provider     string           `json:"provider,omitempty"`
	Action       string           `json:"action,omitempty"`
	Args         JSONObject       `json:"args"`
	Outcome      ExecutionOutcome `json:"outcome,omitempty"`
	FailureStage FailureStage     `json:"failure_stage,omitempty"`
	Error        string           `json:"error,omitempty"`
	Result       JSONValue        `json:"result,omitempty"`
}

// TraceCounts records projection losses in the bounded durable trace.
type TraceCounts struct {
	DroppedValues     int `json:"dropped_values"`
	TruncatedValues   int `json:"truncated_values"`
	RedactedValues    int `json:"redacted_values"`
	DroppedOperations int `json:"dropped_operations"`
}

// ExecutionTrace is the deterministic durable trace envelope.
type ExecutionTrace struct {
	Kind       string           `json:"kind"`
	Version    int              `json:"version"`
	Outcome    ExecutionOutcome `json:"outcome"`
	Phases     []string         `json:"phases"`
	Operations []TraceOperation `json:"operations"`
	Counts     TraceCounts      `json:"counts"`
	Error      string           `json:"error,omitempty"`
}

// TraceRecorder records issue order independently of completion order.
type TraceRecorder struct {
	mu         sync.Mutex
	sealed     bool
	phases     []string
	operations []*TraceOperation
}

// CallCompletion is the terminal data for one nested action.
type CallCompletion struct {
	Outcome      ExecutionOutcome
	Provider     string
	Action       string
	FailureStage FailureStage
	Error        string
	Result       JSONValue
}

// CallTraceHandle completes one issued call exactly once.
type CallTraceHandle struct {
	recorder *TraceRecorder
	index    int
	once     sync.Once
}

// NewTraceRecorder creates an empty execution trace recorder.
func NewTraceRecorder() *TraceRecorder {
	return &TraceRecorder{}
}

// Phase appends a deterministic execution phase before sealing.
func (r *TraceRecorder) Phase(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return
	}
	r.phases = append(r.phases, name)
}

// BeginCall assigns sequence at issue time.
func (r *TraceRecorder) BeginCall(ref string, args JSONObject) *CallTraceHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return &CallTraceHandle{}
	}
	index := len(r.operations)
	r.operations = append(r.operations, &TraceOperation{
		Type:     "call",
		Sequence: index + 1,
		Ref:      ref,
		Args:     cloneJSONObject(args),
	})
	return &CallTraceHandle{recorder: r, index: index}
}

// Complete records the call terminal state. Repeated completions are ignored.
func (h *CallTraceHandle) Complete(completion CallCompletion) {
	if h == nil || h.recorder == nil {
		return
	}
	h.once.Do(func() {
		h.recorder.mu.Lock()
		defer h.recorder.mu.Unlock()
		if h.index < 0 || h.index >= len(h.recorder.operations) {
			return
		}
		operation := h.recorder.operations[h.index]
		operation.Outcome = completion.Outcome
		operation.Provider = completion.Provider
		operation.Action = completion.Action
		operation.FailureStage = completion.FailureStage
		operation.Error = completion.Error
		operation.Result = cloneJSONValue(completion.Result)
	})
}

// Seal returns an immutable snapshot. The first seal fixes the recorder.
func (r *TraceRecorder) Seal(outcome ExecutionOutcome, message string) ExecutionTrace {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealed = true
	operations := make([]TraceOperation, len(r.operations))
	for i, operation := range r.operations {
		operations[i] = *operation
		operations[i].Args = cloneJSONObject(operation.Args)
		operations[i].Result = cloneJSONValue(operation.Result)
	}
	return ExecutionTrace{
		Kind:       "ultra.fabric.execution",
		Version:    1,
		Outcome:    outcome,
		Phases:     slices.Clone(r.phases),
		Operations: operations,
		Error:      message,
	}
}

func cloneJSONObject(value JSONObject) JSONObject {
	if value == nil {
		return nil
	}
	clone, _ := cloneJSONValue(value).(map[string]any)
	return clone
}

func cloneJSONValue(value JSONValue) JSONValue {
	switch value := value.(type) {
	case map[string]any:
		clone := make(map[string]any, len(value))
		for key, item := range value {
			clone[key] = cloneJSONValue(item)
		}
		return clone
	case []any:
		clone := make([]any, len(value))
		for i, item := range value {
			clone[i] = cloneJSONValue(item)
		}
		return clone
	default:
		return value
	}
}
