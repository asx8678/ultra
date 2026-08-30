package gojasandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
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

type gojaCompletion struct {
	resolve func(any) error
	reject  func(any) error
	value   fabric.JSONValue
	err     error
}

type gojaCallDispatcher struct {
	ctx         context.Context
	request     fabric.SandboxExecutionRequest
	completions chan gojaCompletion
	calls       sync.WaitGroup
	issued      int
}

func newGojaCallDispatcher(ctx context.Context, request fabric.SandboxExecutionRequest) *gojaCallDispatcher {
	return &gojaCallDispatcher{
		ctx:         ctx,
		request:     request,
		completions: make(chan gojaCompletion, 256),
	}
}

func (d *gojaCallDispatcher) invoke(vm *goja.Runtime, ref string, args fabric.JSONObject) goja.Value {
	promise, resolve, reject := vm.NewPromise()
	if d.issued >= fabric.MaxNestedCalls {
		_ = reject(vm.NewGoError(fmt.Errorf("nested call limit %d reached", fabric.MaxNestedCalls)))
		return vm.ToValue(promise)
	}
	d.issued++
	d.calls.Add(1)
	go func() {
		defer d.calls.Done()
		var value fabric.JSONValue
		var err error
		if d.request.Bridge == nil {
			err = errors.New("fabric call bridge is unavailable")
		} else {
			value, err = d.request.Bridge.Call(d.ctx, ref, args)
		}
		select {
		case d.completions <- gojaCompletion{resolve: resolve, reject: reject, value: value, err: err}:
		case <-d.ctx.Done():
		}
	}()
	return vm.ToValue(promise)
}

func (d *gojaCallDispatcher) wait() { d.calls.Wait() }

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

// NewGojaSandbox creates a restricted JavaScript sandbox.
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

	var executionCtx context.Context
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		executionCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	} else {
		executionCtx, cancel = context.WithCancel(ctx)
	}
	dispatcher := newGojaCallDispatcher(executionCtx, request)
	defer func() {
		cancel()
		dispatcher.wait()
	}()

	for _, name := range []string{
		"process", "require", "fetch", "XMLHttpRequest", "WebSocket",
		"Deno", "Bun", "load", "read", "readFile", "std", "os",
	} {
		if err := vm.Set(name, goja.Undefined()); err != nil {
			err = fmt.Errorf("remove ambient capability %q: %w", name, err)
			return gojaErrorResult(err), err
		}
	}
	logs := newBoundedGojaLogs(request.MaxLogBytes)
	if err := installGojaFabricBridge(vm, request, logs, dispatcher); err != nil {
		return gojaErrorResult(err), err
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
	go monitorGojaExecution(executionCtx, vm, limit, stop, &interruptErr)
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
	if err := executionCtx.Err(); err != nil {
		result := gojaErrorResult(err)
		result.Logs = logs.values
		return result, err
	}
	resultValue, err := settledGojaValue(executionCtx, vm, value, dispatcher)
	if err != nil {
		if contextErr := executionCtx.Err(); contextErr != nil {
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
	vm *goja.Runtime,
	request fabric.SandboxExecutionRequest,
	logs *boundedGojaLogs,
	dispatcher *gojaCallDispatcher,
) error {
	bridge := vm.NewObject()
	if err := bridge.Set("call", func(call goja.FunctionCall) goja.Value {
		ref := call.Argument(0).String()
		args, err := gojaCallArgs(call.Argument(1))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return dispatcher.invoke(vm, ref, args)
	}); err != nil {
		return fmt.Errorf("install Fabric call bridge: %w", err)
	}
	if err := bridge.Set("literal", func(name string) goja.Value {
		return vm.ToValue(request.Strings[name])
	}); err != nil {
		return fmt.Errorf("install Fabric literal bridge: %w", err)
	}
	if err := bridge.Set("progress", func(call goja.FunctionCall) goja.Value {
		normalized, err := normalizeGojaJSON(call.Argument(0).Export())
		if err != nil {
			panic(vm.NewGoError(err))
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		var update fabric.ActivityUpdate
		if err := json.Unmarshal(encoded, &update); err != nil {
			panic(vm.NewGoError(err))
		}
		if update.Kind == "" {
			panic(vm.NewGoError(errors.New("fabric progress kind is required")))
		}
		if request.Bridge == nil {
			panic(vm.NewGoError(errors.New("fabric progress bridge is unavailable")))
		}
		if err := request.Bridge.Progress(update); err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	}); err != nil {
		return fmt.Errorf("install Fabric progress bridge: %w", err)
	}
	if err := bridge.Set("log", func(value goja.Value) {
		logs.append(value.String())
	}); err != nil {
		return fmt.Errorf("install Fabric log bridge: %w", err)
	}
	if err := vm.Set("fabric", bridge); err != nil {
		return fmt.Errorf("install Fabric bridge: %w", err)
	}
	if _, err := vm.RunString(`Object.freeze(globalThis.fabric);`); err != nil {
		return fmt.Errorf("freeze Fabric bridge: %w", err)
	}
	if err := installGojaNamedStrings(vm, request.Strings); err != nil {
		return err
	}
	if err := installGojaCapabilityGlobals(vm, request.Bindings, dispatcher); err != nil {
		return err
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
	if err := vm.Set("console", console); err != nil {
		return err
	}
	if _, err := vm.RunString(`Object.freeze(globalThis.console);`); err != nil {
		return fmt.Errorf("freeze console bridge: %w", err)
	}
	return nil
}

func gojaCallArgs(value goja.Value) (fabric.JSONObject, error) {
	if goja.IsUndefined(value) || goja.IsNull(value) {
		return fabric.JSONObject{}, nil
	}
	normalized, err := normalizeGojaJSON(value.Export())
	if err != nil {
		return nil, err
	}
	args, ok := normalized.(map[string]any)
	if !ok {
		return nil, errors.New("fabric call arguments must be an object")
	}
	return args, nil
}

func installGojaNamedStrings(vm *goja.Runtime, values map[string]string) error {
	literal := vm.NewObject()
	for name, value := range values {
		if err := literal.Set(name, value); err != nil {
			return fmt.Errorf("install Fabric named string %q: %w", name, err)
		}
	}
	if err := vm.Set("π", literal); err != nil {
		return fmt.Errorf("install Fabric named strings: %w", err)
	}
	_, err := vm.RunString(`Object.freeze(globalThis["π"]);`)
	if err != nil {
		return fmt.Errorf("freeze Fabric named strings: %w", err)
	}
	return nil
}

func installGojaCapabilityGlobals(
	vm *goja.Runtime,
	bindings []fabric.CapabilityBinding,
	dispatcher *gojaCallDispatcher,
) error {
	providers := make(map[string]map[string]string)
	for _, binding := range bindings {
		provider, action, found := strings.Cut(binding.Ref, ".")
		if !found {
			continue
		}
		if provider == "fabric" || provider == "console" || provider == "π" {
			return fmt.Errorf("fabric provider %q conflicts with a sandbox global", provider)
		}
		if providers[provider] == nil {
			providers[provider] = make(map[string]string)
		}
		providers[provider][action] = binding.Ref
	}
	providerNames := make([]string, 0, len(providers))
	for provider := range providers {
		providerNames = append(providerNames, provider)
	}
	slices.Sort(providerNames)
	for _, provider := range providerNames {
		actions := providers[provider]
		providerObject := vm.NewObject()
		actionNames := make([]string, 0, len(actions))
		for action := range actions {
			actionNames = append(actionNames, action)
		}
		slices.Sort(actionNames)
		for _, action := range actionNames {
			ref := actions[action]
			if err := providerObject.Set(action, func(call goja.FunctionCall) goja.Value {
				args, err := gojaCallArgs(call.Argument(0))
				if err != nil {
					panic(vm.NewGoError(err))
				}
				return dispatcher.invoke(vm, ref, args)
			}); err != nil {
				return fmt.Errorf("install Fabric capability %q: %w", ref, err)
			}
		}
		if err := vm.Set(provider, providerObject); err != nil {
			return fmt.Errorf("install Fabric provider %q: %w", provider, err)
		}
		if _, err := vm.RunString(fmt.Sprintf(`Object.freeze(globalThis[%q]);`, provider)); err != nil {
			return fmt.Errorf("freeze Fabric provider %q: %w", provider, err)
		}
	}
	return nil
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

func settledGojaValue(
	ctx context.Context,
	vm *goja.Runtime,
	value goja.Value,
	dispatcher *gojaCallDispatcher,
) (goja.Value, error) {
	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		return value, nil
	}
	for promise.State() == goja.PromiseStatePending {
		select {
		case completion := <-dispatcher.completions:
			var err error
			if completion.err != nil {
				err = completion.reject(vm.NewGoError(completion.err))
			} else {
				err = completion.resolve(completion.value)
			}
			if err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if promise.State() == goja.PromiseStateRejected {
		return nil, fmt.Errorf("fabric JavaScript rejected: %s", promise.Result().String())
	}
	return promise.Result(), nil
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
