package fabric

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type authorizerFunc func(context.Context, AuthorizationRequest) (AuthorizationDecision, error)

func (f authorizerFunc) Authorize(ctx context.Context, request AuthorizationRequest) (AuthorizationDecision, error) {
	return f(ctx, request)
}

type approvalFunc func(context.Context, ApprovalRequest) (ApprovalDecision, error)

func (f approvalFunc) Decide(ctx context.Context, request ApprovalRequest) (ApprovalDecision, error) {
	return f(ctx, request)
}

type pipelineProvider struct {
	registryTestProvider
	prepare func(context.Context, JSONObject) (JSONObject, error)
	invoke  func(context.Context, JSONObject) (JSONValue, error)
}

func (p *pipelineProvider) PrepareArguments(
	ctx context.Context,
	_ string,
	args JSONObject,
	_ InvocationContext,
) (JSONObject, error) {
	if p.prepare == nil {
		return args, nil
	}
	return p.prepare(ctx, args)
}

func (p *pipelineProvider) Invoke(
	ctx context.Context,
	_ string,
	args JSONObject,
	_ InvocationContext,
) (JSONValue, error) {
	if p.invoke == nil {
		return args, nil
	}
	return p.invoke(ctx, args)
}

func pipelineDescriptor() ActionDescriptor {
	return ActionDescriptor{
		Name:        "run",
		Description: "Run a pipeline test action.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"value":{"type":"string"},
				"prepared":{"type":"boolean"}
			},
			"required":["value","prepared"],
			"additionalProperties":false
		}`),
		Risk:   RiskWrite,
		Effect: &EffectDescriptor{Kind: EffectTransactional, Resources: []string{"workspace"}},
	}
}

func setupPipeline(t *testing.T, provider *pipelineProvider) (*Registry, *CapabilityView) {
	t.Helper()
	registry := NewRegistry()
	provider.name = "host"
	provider.actions = []ActionDescriptor{pipelineDescriptor()}
	lease, err := registry.RegisterProvider(t.Context(), provider, RegisterOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Dispose()) })
	view, err := registry.AcquireLiveView()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Release()) })
	return registry, view
}

func TestInvokePipelineOrder(t *testing.T) {
	t.Parallel()

	var eventsMu sync.Mutex
	var events []string
	record := func(event string) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}
	provider := &pipelineProvider{
		prepare: func(_ context.Context, args JSONObject) (JSONObject, error) {
			record("prepare")
			args["prepared"] = true
			return args, nil
		},
		invoke: func(_ context.Context, args JSONObject) (JSONValue, error) {
			record("invoke")
			return JSONObject{"echo": args["value"]}, nil
		},
	}
	registry, view := setupPipeline(t, provider)
	trace := NewTraceRecorder()
	result, err := registry.Invoke(t.Context(), InvokeRequest{
		View: view,
		Ref:  "host.run",
		Args: JSONObject{"value": "ok"},
		Authorizer: authorizerFunc(func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			record("authorize")
			return AuthorizationDecision{Allowed: true}, nil
		}),
		Approvals: approvalFunc(func(context.Context, ApprovalRequest) (ApprovalDecision, error) {
			record("approve")
			return ApprovalDecision{Kind: ApprovalAllow, Scope: ApprovalOnce}, nil
		}),
		Trace: trace,
	})
	require.NoError(t, err)
	require.Equal(t, JSONObject{"echo": "ok"}, result)
	require.Equal(t, []string{"authorize", "prepare", "approve", "invoke"}, events)
	sealed := trace.Seal(OutcomeSucceeded, "")
	require.Len(t, sealed.Operations, 1)
	require.Equal(t, OutcomeSucceeded, sealed.Operations[0].Outcome)
}

func TestInvokeDeniedHasNoSideEffect(t *testing.T) {
	t.Parallel()

	var prepared atomic.Bool
	var invoked atomic.Bool
	provider := &pipelineProvider{
		prepare: func(_ context.Context, args JSONObject) (JSONObject, error) {
			prepared.Store(true)
			return args, nil
		},
		invoke: func(context.Context, JSONObject) (JSONValue, error) {
			invoked.Store(true)
			return nil, nil
		},
	}
	registry, view := setupPipeline(t, provider)
	_, err := registry.Invoke(t.Context(), InvokeRequest{
		View: view,
		Ref:  "host.run",
		Args: JSONObject{"value": "blocked"},
		Authorizer: authorizerFunc(func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			return AuthorizationDecision{Allowed: false, Reason: "test policy"}, nil
		}),
		Approvals: approvalFunc(func(context.Context, ApprovalRequest) (ApprovalDecision, error) {
			t.Fatal("approval must not run after authorization denial")
			return ApprovalDecision{}, nil
		}),
	})
	require.ErrorIs(t, err, ErrUnauthorized)
	require.False(t, prepared.Load())
	require.False(t, invoked.Load())
	var invocationErr *InvocationError
	require.ErrorAs(t, err, &invocationErr)
	require.Equal(t, FailureAuthorize, invocationErr.Stage)
}

func TestInvokeUsesPreparedArgsForApproval(t *testing.T) {
	t.Parallel()

	var approved JSONObject
	var invoked JSONObject
	provider := &pipelineProvider{
		prepare: func(_ context.Context, args JSONObject) (JSONObject, error) {
			args["prepared"] = true
			return args, nil
		},
		invoke: func(_ context.Context, args JSONObject) (JSONValue, error) {
			invoked = cloneJSONObject(args)
			return JSONObject{"ok": true}, nil
		},
	}
	registry, view := setupPipeline(t, provider)
	original := JSONObject{"value": "original"}
	_, err := registry.Invoke(t.Context(), InvokeRequest{
		View: view,
		Ref:  "host.run",
		Args: original,
		Authorizer: authorizerFunc(func(_ context.Context, request AuthorizationRequest) (AuthorizationDecision, error) {
			require.NotContains(t, request.Args, "prepared")
			return AuthorizationDecision{Allowed: true}, nil
		}),
		Approvals: approvalFunc(func(_ context.Context, request ApprovalRequest) (ApprovalDecision, error) {
			approved = cloneJSONObject(request.PreparedArgs)
			request.PreparedArgs["value"] = "approval mutation"
			return ApprovalDecision{Kind: ApprovalAllow, Scope: ApprovalOnce}, nil
		}),
	})
	require.NoError(t, err)
	require.Equal(t, JSONObject{"value": "original", "prepared": true}, approved)
	require.Equal(t, approved, invoked)
	require.Equal(t, JSONObject{"value": "original"}, original)
}

func TestInvokeCancellationPropagates(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	provider := &pipelineProvider{
		prepare: func(_ context.Context, args JSONObject) (JSONObject, error) {
			args["prepared"] = true
			return args, nil
		},
		invoke: func(ctx context.Context, _ JSONObject) (JSONValue, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	registry, view := setupPipeline(t, provider)
	ctx, cancel := context.WithCancel(t.Context())
	trace := NewTraceRecorder()
	done := make(chan error, 1)
	go func() {
		_, err := registry.Invoke(ctx, InvokeRequest{
			View: view,
			Ref:  "host.run",
			Args: JSONObject{"value": "wait"},
			Authorizer: authorizerFunc(func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
				return AuthorizationDecision{Allowed: true}, nil
			}),
			Approvals: approvalFunc(func(context.Context, ApprovalRequest) (ApprovalDecision, error) {
				return ApprovalDecision{Kind: ApprovalAllow, Scope: ApprovalOnce}, nil
			}),
			Trace: trace,
		})
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("provider did not observe cancellation")
	}
	sealed := trace.Seal(OutcomeAborted, "cancelled")
	require.Equal(t, OutcomeAborted, sealed.Operations[0].Outcome)
	require.Equal(t, FailureInvoke, sealed.Operations[0].FailureStage)
}
