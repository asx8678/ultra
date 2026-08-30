package fabric

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Args         JSONObject       `json:"args,omitempty"`
	ArgsDigest   string           `json:"args_digest,omitempty"`
	Outcome      ExecutionOutcome `json:"outcome,omitempty"`
	FailureStage FailureStage     `json:"failure_stage,omitempty"`
	Error        string           `json:"error,omitempty"`
	Result       JSONValue        `json:"result,omitempty"`
	ResultDigest string           `json:"result_digest,omitempty"`
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

const (
	maxTraceStringBytes = 2048
	maxTraceOperations  = MaxNestedCalls + 1
)

// TraceRecorder records issue order independently of completion order.
type TraceRecorder struct {
	mu         sync.Mutex
	sealed     bool
	phases     []string
	operations []*TraceOperation
	counts     TraceCounts
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
	if len(r.operations) >= maxTraceOperations {
		r.counts.DroppedOperations++
		return &CallTraceHandle{}
	}
	index := len(r.operations)
	ref, truncated := projectTraceString(ref)
	if truncated {
		r.counts.TruncatedValues++
	}
	operation := &TraceOperation{
		Type:       "call",
		Sequence:   index + 1,
		Ref:        ref,
		ArgsDigest: digestTraceValue(args),
	}
	if len(args) > 0 {
		r.counts.RedactedValues++
	}
	r.operations = append(r.operations, operation)
	return &CallTraceHandle{recorder: r, index: index}
}

// SetArgs fingerprints the authoritative prepared arguments without retaining payloads.
func (h *CallTraceHandle) SetArgs(args JSONObject) {
	if h == nil || h.recorder == nil {
		return
	}
	h.recorder.mu.Lock()
	defer h.recorder.mu.Unlock()
	if h.index < 0 || h.index >= len(h.recorder.operations) || h.recorder.sealed {
		return
	}
	h.recorder.operations[h.index].ArgsDigest = digestTraceValue(args)
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
		operation.Error, _ = projectTraceString(completion.Error)
		if operation.Error != completion.Error {
			h.recorder.counts.TruncatedValues++
		}
		if completion.Result != nil {
			operation.ResultDigest = digestTraceValue(completion.Result)
			h.recorder.counts.RedactedValues++
		}
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
	message, truncated := projectTraceString(message)
	if truncated {
		r.counts.TruncatedValues++
	}
	return ExecutionTrace{
		Kind:       "ultra.fabric.execution",
		Version:    1,
		Outcome:    outcome,
		Phases:     slices.Clone(r.phases),
		Operations: operations,
		Counts:     r.counts,
		Error:      message,
	}
}

func projectTraceString(value string) (string, bool) {
	if len(value) <= maxTraceStringBytes {
		return value, false
	}
	return value[:maxTraceStringBytes] + "…", true
}

func digestTraceValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
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
