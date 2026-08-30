package gojasandbox

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asx8678/ultra/internal/fabric"
	"github.com/stretchr/testify/require"
)

func TestSandboxFreshContext(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	first, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript: `globalThis.fabricSecret = 42; return fabricSecret;`,
	})
	require.NoError(t, err)
	require.Equal(t, float64(42), first.Value)
	second, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript: `return typeof fabricSecret;`,
	})
	require.NoError(t, err)
	require.Equal(t, "undefined", second.Value)
}

func TestSandboxInstallsPinnedCapabilitiesAndNamedStrings(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	bridge := &recordingSandboxBridge{}
	output, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript: `return { result: await host.view({ path: π.path }), frozen: Object.isFrozen(π) && Object.isFrozen(host) };`,
		Strings:    map[string]string{"path": "README.md"},
		Bindings:   []fabric.CapabilityBinding{{Ref: "host.view"}},
		Bridge:     bridge,
	})
	require.NoError(t, err)
	require.Equal(t, fabric.OutcomeSucceeded, output.Outcome)
	require.Equal(t, []string{"host.view:README.md"}, bridge.calls)
	require.Equal(t, map[string]any{
		"result": map[string]any{"ok": true}, "frozen": true,
	}, output.Value)
}

type recordingSandboxBridge struct {
	calls   []string
	updates []fabric.ActivityUpdate
}

func (b *recordingSandboxBridge) Call(
	_ context.Context,
	ref string,
	args fabric.JSONObject,
) (fabric.JSONValue, error) {
	b.calls = append(b.calls, ref+":"+args["path"].(string))
	return fabric.JSONObject{"ok": true}, nil
}

func (b *recordingSandboxBridge) Progress(update fabric.ActivityUpdate) error {
	b.updates = append(b.updates, update)
	return nil
}

func TestSandboxPublishesGuestProgress(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	bridge := &recordingSandboxBridge{}
	output, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript: `fabric.progress({ kind: "status", message: "indexing", data: { files: 3 } }); return true;`,
		Bridge:     bridge,
	})
	require.NoError(t, err)
	require.Equal(t, fabric.OutcomeSucceeded, output.Outcome)
	require.Len(t, bridge.updates, 1)
	require.Equal(t, "status", bridge.updates[0].Kind)
	require.Equal(t, "indexing", bridge.updates[0].Message)
	require.Equal(t, float64(3), bridge.updates[0].Data["files"])
}

func TestSandboxNoAmbientAuthority(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	output, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript: `
			return {
				process: typeof process,
				require: typeof require,
				fetch: typeof fetch,
				websocket: typeof WebSocket,
				filesystem: typeof readFile,
				fabric: typeof fabric.call
			};
		`,
		MemoryLimitBytes: 8 << 20,
	})
	require.NoError(t, err)
	require.Equal(t, fabric.OutcomeSucceeded, output.Outcome)
	object, ok := output.Value.(map[string]any)
	require.True(t, ok)
	for _, capability := range []string{"process", "require", "fetch", "websocket", "filesystem"} {
		require.Equal(t, "undefined", object[capability], capability)
	}
	require.Equal(t, "function", object["fabric"])
}

type blockingSandboxBridge struct{ called atomic.Bool }

func (b *blockingSandboxBridge) Call(
	ctx context.Context,
	_ string,
	_ fabric.JSONObject,
) (fabric.JSONValue, error) {
	b.called.Store(true)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*blockingSandboxBridge) Progress(fabric.ActivityUpdate) error { return nil }

func TestSandboxRequestTimeoutInterruptsGuest(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	started := time.Now()
	output, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript: `for (;;) {}`,
		Timeout:    40 * time.Millisecond,
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, fabric.OutcomeTimedOut, output.Outcome)
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestSandboxTimeoutCancelsHostCalls(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	bridge := &blockingSandboxBridge{}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	output, err := sandbox.Execute(ctx, fabric.SandboxExecutionRequest{
		JavaScript: `return fabric.call("host.wait", {});`,
		Bridge:     bridge,
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, fabric.OutcomeTimedOut, output.Outcome)
	require.True(t, bridge.called.Load())
	require.Less(t, time.Since(started), 2*time.Second)
}

type parallelSandboxBridge struct {
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (b *parallelSandboxBridge) Call(
	ctx context.Context,
	_ string,
	_ fabric.JSONObject,
) (fabric.JSONValue, error) {
	active := b.active.Add(1)
	defer b.active.Add(-1)
	for {
		peak := b.peak.Load()
		if active <= peak || b.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	b.started <- struct{}{}
	select {
	case <-b.release:
		return true, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*parallelSandboxBridge) Progress(fabric.ActivityUpdate) error { return nil }

func TestSandboxParallelCallsOverlap(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	bridge := &parallelSandboxBridge{started: make(chan struct{}, 2), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
			JavaScript: `return Promise.all([host.view({}), host.view({})]);`,
			Bindings:   []fabric.CapabilityBinding{{Ref: "host.view"}},
			Bridge:     bridge,
		})
		done <- err
	}()
	for range 2 {
		select {
		case <-bridge.started:
		case <-time.After(2 * time.Second):
			t.Fatal("parallel Fabric calls did not both start")
		}
	}
	require.Equal(t, int32(2), bridge.peak.Load())
	close(bridge.release)
	require.NoError(t, <-done)
}

type countingSandboxBridge struct{ calls atomic.Int32 }

func (b *countingSandboxBridge) Call(
	context.Context,
	string,
	fabric.JSONObject,
) (fabric.JSONValue, error) {
	b.calls.Add(1)
	return true, nil
}

func (*countingSandboxBridge) Progress(fabric.ActivityUpdate) error { return nil }

func TestSandboxBoundsIssuedNestedCalls(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	bridge := &countingSandboxBridge{}
	output, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript: `return Promise.all(Array.from({ length: 129 }, () => host.view({})));`,
		Bindings:   []fabric.CapabilityBinding{{Ref: "host.view"}},
		Bridge:     bridge,
	})
	require.ErrorContains(t, err, "nested call limit")
	require.Equal(t, fabric.OutcomeFailed, output.Outcome)
	require.LessOrEqual(t, bridge.calls.Load(), int32(fabric.MaxNestedCalls))
}

func TestGojaSandboxBoundsLogs(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	output, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{
		JavaScript:  `fabric.log("123456789"); console.log("ignored"); return true;`,
		MaxLogBytes: 8,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"12345678"}, output.Logs)
}

func TestGojaSandboxCancellation(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	t.Cleanup(func() { require.NoError(t, sandbox.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	output, err := sandbox.Execute(ctx, fabric.SandboxExecutionRequest{JavaScript: `while (true) {}`})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, fabric.OutcomeTimedOut, output.Outcome)
}

func TestGojaSandboxCloseIdempotent(t *testing.T) {
	t.Parallel()
	sandbox := NewGojaSandbox()
	require.NoError(t, sandbox.Close())
	require.NoError(t, sandbox.Close())
	_, err := sandbox.Execute(t.Context(), fabric.SandboxExecutionRequest{JavaScript: `return 1;`})
	require.ErrorIs(t, err, errGojaSandboxClosed)
	require.True(t, errors.Is(err, errGojaSandboxClosed))
}
