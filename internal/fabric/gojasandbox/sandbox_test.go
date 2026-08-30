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
