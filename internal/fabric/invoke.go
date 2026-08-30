package fabric

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/kaptinlin/jsonschema"
)

var (
	// ErrUnauthorized reports a denied action before provider preparation.
	ErrUnauthorized = errors.New("fabric action unauthorized")
	// ErrApprovalDenied reports a denied or escalated prepared invocation.
	ErrApprovalDenied = errors.New("fabric action approval denied")
	// ErrEffectConflict reports unsafe concurrent effects.
	ErrEffectConflict = errors.New("fabric action effect conflict")
	// ErrBudgetExhausted reports a nested-call budget refusal.
	ErrBudgetExhausted = errors.New("fabric execution budget exhausted")
)

// AuthorizationDecision is the result of the host authority gate.
type AuthorizationDecision struct {
	Allowed bool
	Reason  string
}

// AuthorizationRequest contains the resolved action and original arguments.
type AuthorizationRequest struct {
	Ref        string
	Descriptor ActionDescriptor
	Args       JSONObject
	Context    InvocationContext
}

// Authorizer gates every resolved invocation before argument preparation.
type Authorizer interface {
	Authorize(context.Context, AuthorizationRequest) (AuthorizationDecision, error)
}

// ApprovalScope describes the lifetime of an interactive approval.
type ApprovalScope string

const (
	ApprovalOnce    ApprovalScope = "once"
	ApprovalSession ApprovalScope = "session"
)

// ApprovalKind is the result class returned by an approval controller.
type ApprovalKind string

const (
	ApprovalAllow    ApprovalKind = "allow"
	ApprovalDeny     ApprovalKind = "deny"
	ApprovalEscalate ApprovalKind = "escalate"
)

// ApprovalDecision is a policy or user decision for prepared arguments.
type ApprovalDecision struct {
	Kind   ApprovalKind
	Scope  ApprovalScope
	Reason string
}

// ApprovalRequest contains authoritative prepared and validated arguments.
type ApprovalRequest struct {
	Ref          string
	Descriptor   ActionDescriptor
	PreparedArgs JSONObject
	Context      InvocationContext
}

// ApprovalController decides whether a prepared invocation may execute.
type ApprovalController interface {
	Decide(context.Context, ApprovalRequest) (ApprovalDecision, error)
}

// BudgetLedger bounds nested calls independently of provider behavior.
type BudgetLedger interface {
	ChargeNestedCall(string) error
}

// InvocationEndedEvent lets a provider observe the outer call's terminal state.
type InvocationEndedEvent struct {
	ExecutionID      string
	ParentToolCallID string
	Outcome          ExecutionOutcome
}

// InvocationEndObserver receives terminal notification after provider work.
type InvocationEndObserver interface {
	InvocationEnded(InvocationEndedEvent)
}

// InvokeRequest contains every authority and lifecycle dependency for one
// registry call.
type InvokeRequest struct {
	View       *CapabilityView
	Ref        string
	Args       JSONObject
	Invocation InvocationContext
	Authorizer Authorizer
	Approvals  ApprovalController
	Budgets    BudgetLedger
	Trace      *TraceRecorder
	Limits     JSONLimits
}

// InvocationError preserves the exact failed registry stage.
type InvocationError struct {
	Ref   string
	Stage FailureStage
	Err   error
}

func (e *InvocationError) Error() string {
	return fmt.Sprintf("fabric %s failed at %s: %v", e.Ref, e.Stage, e.Err)
}

func (e *InvocationError) Unwrap() error {
	return e.Err
}

type activeEffect struct {
	ref    string
	effect EffectDescriptor
}

// Invoke runs one nested call through the authoritative policy pipeline.
func (r *Registry) Invoke(ctx context.Context, request InvokeRequest) (result JSONValue, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args := cloneJSONObject(request.Args)
	trace := request.Trace
	if trace == nil {
		trace = NewTraceRecorder()
	}
	handle := trace.BeginCall(request.Ref, args)
	outcome := OutcomeFailed
	stage := FailureResolve
	var provider Provider
	defer func() {
		if provider != nil {
			if observer, ok := provider.(InvocationEndObserver); ok {
				observer.InvocationEnded(InvocationEndedEvent{
					ExecutionID:      request.Invocation.ExecutionID,
					ParentToolCallID: request.Invocation.ParentToolCallID,
					Outcome:          outcome,
				})
			}
		}
		completion := CallCompletion{Outcome: outcome}
		if invocationErr := (*InvocationError)(nil); errors.As(err, &invocationErr) {
			completion.FailureStage = invocationErr.Stage
			completion.Error = invocationErr.Err.Error()
		} else if err != nil {
			completion.FailureStage = stage
			completion.Error = err.Error()
		} else {
			completion.Result = result
		}
		handle.Complete(completion)
	}()

	binding, releaseCall, resolveErr := r.acquireAction(request.View, request.Ref)
	if resolveErr != nil {
		err = &InvocationError{Ref: request.Ref, Stage: FailureResolve, Err: resolveErr}
		return nil, err
	}
	defer releaseCall()
	provider = binding.binding.provider
	request.Invocation.Context = ctx

	if request.Budgets != nil {
		stage = FailureGuard
		if chargeErr := request.Budgets.ChargeNestedCall(request.Ref); chargeErr != nil {
			err = &InvocationError{Ref: request.Ref, Stage: stage, Err: fmt.Errorf("%w: %v", ErrBudgetExhausted, chargeErr)}
			return nil, err
		}
	}

	stage = FailureAuthorize
	if request.Authorizer == nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: fmt.Errorf("%w: no authorizer", ErrUnauthorized)}
		return nil, err
	}
	decision, authorizeErr := request.Authorizer.Authorize(ctx, AuthorizationRequest{
		Ref:        request.Ref,
		Descriptor: cloneDescriptor(binding.descriptor),
		Args:       cloneJSONObject(args),
		Context:    request.Invocation,
	})
	if authorizeErr != nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: authorizeErr}
		return nil, err
	}
	if !decision.Allowed {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "denied"
		}
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: fmt.Errorf("%w: %s", ErrUnauthorized, reason)}
		return nil, err
	}

	prepared := args
	if preparer, ok := provider.(ArgumentPreparer); ok {
		stage = FailurePrepare
		prepared, err = preparer.PrepareArguments(ctx, actionFromRef(request.Ref), cloneJSONObject(args), request.Invocation)
		if err != nil {
			err = &InvocationError{Ref: request.Ref, Stage: stage, Err: err}
			return nil, err
		}
		prepared = cloneJSONObject(prepared)
	}

	stage = FailureValidate
	if validationErr := ValidateJSON(prepared, request.Limits); validationErr != nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: validationErr}
		return nil, err
	}
	if validationErr := validateSchema(binding.schema, prepared); validationErr != nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: validationErr}
		return nil, err
	}

	stage = FailureApprove
	if request.Approvals == nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: fmt.Errorf("%w: no approval controller", ErrApprovalDenied)}
		return nil, err
	}
	approval, approvalErr := request.Approvals.Decide(ctx, ApprovalRequest{
		Ref:          request.Ref,
		Descriptor:   cloneDescriptor(binding.descriptor),
		PreparedArgs: cloneJSONObject(prepared),
		Context:      request.Invocation,
	})
	if approvalErr != nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: approvalErr}
		return nil, err
	}
	if approval.Kind != ApprovalAllow {
		reason := strings.TrimSpace(approval.Reason)
		if reason == "" {
			reason = string(approval.Kind)
		}
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: fmt.Errorf("%w: %s", ErrApprovalDenied, reason)}
		return nil, err
	}

	stage = FailureEffect
	releaseEffect, effectErr := r.acquireEffect(request.Ref, binding.descriptor.Effect)
	if effectErr != nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: effectErr}
		return nil, err
	}
	defer releaseEffect()

	stage = FailureInvoke
	result, err = provider.Invoke(ctx, actionFromRef(request.Ref), cloneJSONObject(prepared), request.Invocation)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			outcome = OutcomeAborted
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			outcome = OutcomeTimedOut
		}
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: err}
		return nil, err
	}

	stage = FailureResult
	if validationErr := ValidateJSON(result, request.Limits); validationErr != nil {
		err = &InvocationError{Ref: request.Ref, Stage: stage, Err: validationErr}
		return nil, err
	}
	outcome = OutcomeSucceeded
	return cloneJSONValue(result), nil
}

func (r *Registry) acquireAction(view *CapabilityView, ref string) (viewBinding, func(), error) {
	if view == nil || view.registry != r || view.record == nil {
		return viewBinding{}, func() {}, ErrViewReleased
	}
	r.mu.Lock()
	if view.record.released || r.views[view.record.id] != view.record {
		r.mu.Unlock()
		return viewBinding{}, func() {}, fmt.Errorf("%w: %s", ErrViewReleased, view.record.id)
	}
	binding, exists := view.record.bindings[ref]
	if !exists {
		r.mu.Unlock()
		return viewBinding{}, func() {}, fmt.Errorf("%w: %s", ErrActionNotFound, ref)
	}
	binding.binding.owners++
	r.mu.Unlock()

	var once sync.Once
	return binding, func() {
		once.Do(func() {
			var closeNow []Provider
			r.mu.Lock()
			binding.binding.owners--
			if binding.binding.retiring && binding.binding.owners == 0 {
				closeNow = append(closeNow, r.retireBindingLocked(binding.binding)...)
			}
			r.mu.Unlock()
			closeProviders(closeNow)
		})
	}, nil
}

func (r *Registry) acquireEffect(ref string, effect *EffectDescriptor) (func(), error) {
	if effect == nil || effect.Kind == EffectNone {
		return func() {}, nil
	}
	candidate := cloneEffect(*effect)
	r.mu.Lock()
	for _, active := range r.activeEffects {
		if effectsConflict(candidate, active.effect) {
			r.mu.Unlock()
			return func() {}, fmt.Errorf("%w: %s conflicts with %s", ErrEffectConflict, ref, active.ref)
		}
	}
	r.nextEffect++
	id := r.nextEffect
	r.activeEffects[id] = activeEffect{ref: ref, effect: candidate}
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.activeEffects, id)
			r.mu.Unlock()
		})
	}, nil
}

func effectsConflict(left, right EffectDescriptor) bool {
	if left.Kind == EffectNone || right.Kind == EffectNone {
		return false
	}
	if left.Kind == EffectUnknown || right.Kind == EffectUnknown {
		return true
	}
	if left.Commutative && right.Commutative {
		return false
	}
	if len(left.Resources) == 0 || len(right.Resources) == 0 {
		return true
	}
	for _, resource := range left.Resources {
		if slices.Contains(right.Resources, resource) {
			return true
		}
	}
	return false
}

func cloneEffect(effect EffectDescriptor) EffectDescriptor {
	effect.Resources = slices.Clone(effect.Resources)
	return effect
}

func validateSchema(schema *jsonschema.Schema, args JSONObject) error {
	if schema == nil {
		return errors.New("fabric action input schema is unavailable")
	}
	result := schema.Validate(args)
	if result.IsValid() {
		return nil
	}
	return fmt.Errorf("input schema validation failed: %v", result.DetailedErrors())
}

func actionFromRef(ref string) string {
	_, action, found := strings.Cut(ref, ".")
	if !found {
		return ref
	}
	return action
}
