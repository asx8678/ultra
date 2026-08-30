package agent

import (
	"context"
	"uuid"
)

// runIDContextKey is the unexported context key used to carry a
// caller-supplied RunID from the workspace HTTP boundary
// (backend.SendMessage) down into coordinator.Run without forcing a
// breaking change to the Coordinator.Run signature. The value is
// then copied onto SessionAgentCall.RunID by the coordinator so the
// agent's terminal RunComplete event can echo it back to the
// originating caller.
type runIDContextKey struct{}

// NewRunID creates the opaque identifier for one submitted turn. Run IDs are
// created at the runtime boundary rather than by a particular frontend so
// in-process, client/server, and future JSONL callers share the same
// completion contract.
func NewRunID() string {
	return uuid.New().String()
}

// EnsureRunID preserves a caller-provided identifier or creates one when the
// caller does not need to correlate the turn itself. This keeps RunID
// mandatory inside the runtime while preserving backwards-compatible request
// payloads at transport boundaries.
func EnsureRunID(runID string) string {
	if runID != "" {
		return runID
	}
	return NewRunID()
}

// WithRunID returns ctx tagged with a per-request RunID. Empty runIDs are
// normalized so downstream code always sees a stable identifier for the
// submitted turn.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDContextKey{}, EnsureRunID(runID))
}

// RunIDFromContext returns the RunID set by [WithRunID], or "" if
// none was set or the value is not a string. Exported because the
// coordinator and tests in other packages need to read it; safe to
// call on any context.
func RunIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(runIDContextKey{}).(string); ok {
		return v
	}
	return ""
}
