package gojasandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asx8678/ultra/internal/fabric"
	"github.com/dop251/goja"
)

const (
	defaultGojaMemoryLimit = int64(64 << 20)
	defaultGojaMaxLogBytes = 64 << 10
	maxGojaLogEntries      = 256
	gojaMemoryPollInterval = 5 * time.Millisecond
)

var errGojaSandboxClosed = errors.New("fabric JavaScript sandbox is closed")

type gojaInterrupt struct{ err error }

type boundedGojaLogs struct {
	values   []string
	bytes    int
	maxBytes int
}

func newBoundedGojaLogs(maxBytes int) *boundedGojaLogs {
	if maxBytes <= 0 {
		maxBytes = defaultGojaMaxLogBytes
	}
	return &boundedGojaLogs{values: make([]string, 0), maxBytes: maxBytes}
}

func (l *boundedGojaLogs) append(value string) {
	if len(l.values) >= maxGojaLogEntries || l.bytes >= l.maxBytes {
		return
	}
	if remaining := l.maxBytes - l.bytes; len(value) > remaining {
		value = value[:remaining]
	}
	l.values = append(l.values, value)
	l.bytes += len(value)
}

// SandboxFeatures reports security-relevant runtime capabilities.
type SandboxFeatures struct {
	Sandboxing           bool
	Cancellation         bool
	HardMemoryLimit      bool
	DeterministicRuntime bool
	Notes                []string
}

// GojaSandbox is a pure-Go, per-execution ECMAScript sandbox. A fresh runtime
// has no process, filesystem, network, module-loader, or timer bindings; only
// the narrow Fabric bridge below is installed.
type GojaSandbox struct {
	mu     sync.Mutex
	active map[uint64]*goja.Runtime
	nextID atomic.Uint64
	closed atomic.Bool
}

// NewGojaSandbox creates an isolated JavaScript sandbox.
func NewGojaSandbox() *GojaSandbox {
	return &GojaSandbox{active: make(map[uint64]*goja.Runtime)}
}

func (s *GojaSandbox) Execute(
	ctx context.Context,
	request fabric.SandboxExecutionRequest,
) (fabric.SandboxExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return gojaErrorResult(err), err
	}
	if request.JavaScript == "" {
		err := errors.New("fabric JavaScript program is empty")
		return gojaErrorResult(err), err
	}
	vm := goja.New()
	runID, err := s.register(vm)
	if err != nil {
		return gojaErrorResult(err), err
	}
	defer s.unregister(runID)

	logs := newBoundedGojaLogs(request.MaxLogBytes)
	if err := installGojaFabricBridge(ctx, vm, request, logs); err != nil {
		return gojaErrorResult(err), err
	}
	for _, name := range []string{
		"process", "require", "fetch", "XMLHttpRequest", "WebSocket",
		"Deno", "Bun", "load", "read", "readFile", "std", "os",
	} {
		if err := vm.Set(name, goja.Undefined()); err != nil {
			err = fmt.Errorf("remove ambient capability %q: %w", name, err)
			return gojaErrorResult(err), err
		}
	}

	limit := request.MemoryLimitBytes
	if limit <= 0 {
		limit = defaultGojaMemoryLimit
	}
	if int64(len(request.JavaScript)) > limit {
		err := errors.New("fabric JavaScript source exceeds memory limit")
		return gojaErrorResult(err), err
	}
	stop := make(chan struct{})
	var interruptErr atomic.Pointer[gojaInterrupt]
	go monitorGojaExecution(ctx, vm, limit, stop, &interruptErr)
	defer close(stop)

	wrapped := "(async function __ultra_fabric_main__() {\n" + request.JavaScript + "\n})()"
	value, runErr := vm.RunString(wrapped)
	if runErr != nil {
		if interrupted := interruptErr.Load(); interrupted != nil {
			result := gojaErrorResult(interrupted.err)
			result.Logs = logs.values
			return result, interrupted.err
		}
		err := fmt.Errorf("execute fabric JavaScript: %w", runErr)
		result := gojaErrorResult(err)
		result.Logs = logs.values
		return result, err
	}
	if err := ctx.Err(); err != nil {
		result := gojaErrorResult(err)
		result.Logs = logs.values
		return result, err
	}
	resultValue, err := settledGojaValue(value)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		}
		result := gojaErrorResult(err)
		result.Logs = logs.values
		return result, err
	}
	normalized, err := normalizeGojaJSON(resultValue.Export())
	if err != nil {
		err = fmt.Errorf("normalize fabric JavaScript result: %w", err)
		result := gojaErrorResult(err)
		result.Logs = logs.values
		return result, err
	}
	return fabric.SandboxExecutionResult{
		Outcome: fabric.OutcomeSucceeded,
		Value:   normalized,
		Logs:    logs.values,
	}, nil
}

func (s *GojaSandbox) register(vm *goja.Runtime) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return 0, errGojaSandboxClosed
	}
	id := s.nextID.Add(1)
	s.active[id] = vm
	return id, nil
}

func (s *GojaSandbox) unregister(id uint64) {
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
}

// Close interrupts active executions and is safe to call repeatedly.
func (s *GojaSandbox) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, vm := range s.active {
		vm.Interrupt(gojaInterrupt{err: errGojaSandboxClosed})
	}
	return nil
}

// Features reports what the VM enforces rather than assuming optional support.
func (*GojaSandbox) Features() SandboxFeatures {
	return SandboxFeatures{
		Sandboxing:           true,
		Cancellation:         true,
		HardMemoryLimit:      false,
		DeterministicRuntime: false,
		Notes: []string{
			"Guest runtime has no ambient filesystem, process, module, timer, or network bindings.",
			"Memory monitoring is best-effort and is not advertised as a hard VM ceiling.",
		},
	}
}

func installGojaFabricBridge(
	ctx context.Context,
	vm *goja.Runtime,
	request fabric.SandboxExecutionRequest,
	logs *boundedGojaLogs,
) error {
	bridge := vm.NewObject()
	if err := bridge.Set("call", func(call goja.FunctionCall) goja.Value {
		if request.Bridge == nil {
			panic(vm.NewGoError(errors.New("fabric call bridge is unavailable")))
		}
		ref := call.Argument(0).String()
		normalized, err := normalizeGojaJSON(call.Argument(1).Export())
		args, ok := normalized.(map[string]any)
		if !ok && err == nil {
			err = errors.New("fabric call arguments must be an object")
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		result, err := request.Bridge.Call(ctx, ref, args)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(result)
	}); err != nil {
		return fmt.Errorf("install Fabric call bridge: %w", err)
	}
	if err := bridge.Set("literal", func(name string) goja.Value {
		return vm.ToValue(request.Strings[name])
	}); err != nil {
		return fmt.Errorf("install Fabric literal bridge: %w", err)
	}
	if err := bridge.Set("tokens", func() int64 { return 0 }); err != nil {
		return fmt.Errorf("install Fabric token bridge: %w", err)
	}
	if err := bridge.Set("log", func(value goja.Value) {
		logs.append(value.String())
	}); err != nil {
		return fmt.Errorf("install Fabric log bridge: %w", err)
	}
	if err := vm.Set("fabric", bridge); err != nil {
		return fmt.Errorf("install Fabric bridge: %w", err)
	}
	console := vm.NewObject()
	if err := console.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]any, 0, len(call.Arguments))
		for _, argument := range call.Arguments {
			parts = append(parts, argument.Export())
		}
		logs.append(fmt.Sprint(parts...))
		return goja.Undefined()
	}); err != nil {
		return fmt.Errorf("install console bridge: %w", err)
	}
	return vm.Set("console", console)
}

func monitorGojaExecution(
	ctx context.Context,
	vm *goja.Runtime,
	memoryLimit int64,
	stop <-chan struct{},
	interruptErr *atomic.Pointer[gojaInterrupt],
) {
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	ticker := time.NewTicker(gojaMemoryPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cause := &gojaInterrupt{err: ctx.Err()}
			if interruptErr.CompareAndSwap(nil, cause) {
				vm.Interrupt(*cause)
			}
			return
		case <-ticker.C:
			var current runtime.MemStats
			runtime.ReadMemStats(&current)
			if current.Alloc > baseline.Alloc+uint64(memoryLimit) {
				cause := &gojaInterrupt{err: errors.New("fabric JavaScript memory limit exceeded")}
				if interruptErr.CompareAndSwap(nil, cause) {
					vm.Interrupt(*cause)
				}
				return
			}
		case <-stop:
			return
		}
	}
}

func settledGojaValue(value goja.Value) (goja.Value, error) {
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		return value, nil
	}
	switch promise.State() {
	case goja.PromiseStateFulfilled:
		return promise.Result(), nil
	case goja.PromiseStateRejected:
		return nil, fmt.Errorf("fabric JavaScript rejected: %s", promise.Result().String())
	default:
		return nil, errors.New("fabric JavaScript left an unsettled promise")
	}
}

func normalizeGojaJSON(value any) (fabric.JSONValue, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", fabric.ErrInvalidJSON, err)
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, fmt.Errorf("%w: %v", fabric.ErrInvalidJSON, err)
	}
	if err := fabric.ValidateJSON(normalized, fabric.JSONLimits{}); err != nil {
		return nil, err
	}
	return normalized, nil
}

func gojaErrorResult(err error) fabric.SandboxExecutionResult {
	outcome := fabric.OutcomeFailed
	if errors.Is(err, context.Canceled) {
		outcome = fabric.OutcomeAborted
	} else if errors.Is(err, context.DeadlineExceeded) {
		outcome = fabric.OutcomeTimedOut
	}
	return fabric.SandboxExecutionResult{Outcome: outcome, Error: err.Error(), Logs: []string{}}
}
