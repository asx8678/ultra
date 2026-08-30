//go:build fabric_sandbox

package agent

import (
	"context"
	"errors"
	"sync"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/fabric"
	"github.com/asx8678/ultra/internal/fabric/esbuildcompiler"
	"github.com/asx8678/ultra/internal/fabric/gojasandbox"
	"github.com/asx8678/ultra/internal/hooks"
	"github.com/asx8678/ultra/internal/permission"
)

var errFabricRuntimeClosed = errors.New("fabric runtime is closed")

type ultraFabricRuntime struct {
	mu       sync.Mutex
	registry *fabric.Registry
	service  *fabric.ExecutionService
	sandbox  *gojasandbox.GojaSandbox
	lease    fabric.Disposable
	closed   bool
}

func newFabricRuntime(permissions permission.Service) (fabricRuntime, error) {
	registry := fabric.NewRegistry()
	sandbox := gojasandbox.NewGojaSandbox()
	runtime := &ultraFabricRuntime{
		registry: registry,
		sandbox:  sandbox,
	}
	runtime.service = &fabric.ExecutionService{
		Registry:   registry,
		Compiler:   esbuildcompiler.New(),
		Sandbox:    sandbox,
		Authorizer: tools.UltraFabricAuthorizer{Permissions: permissions},
		Approvals:  tools.UltraFabricApprovals{},
		Limits:     fabric.DefaultJSONLimits(),
	}
	return runtime, nil
}

func (r *ultraFabricRuntime) Execute(
	ctx context.Context,
	request fabric.FabricExecRequest,
	outer fabric.OuterInvocationContext,
) fabric.FabricExecResult {
	return r.service.Execute(ctx, request, outer)
}

func (r *ultraFabricRuntime) ReplaceNativeTools(
	ctx context.Context,
	nativeTools []fantasy.AgentTool,
	hookRunner *hooks.Runner,
) error {
	provider, err := tools.NewUltraFabricProviderWithHooks(nativeTools, hookRunner)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errFabricRuntimeClosed
	}
	lease, err := r.registry.RegisterProvider(ctx, provider, fabric.RegisterOptions{Replace: true})
	if err != nil {
		r.mu.Unlock()
		return err
	}
	previous := r.lease
	r.lease = lease
	r.mu.Unlock()
	if previous != nil {
		_ = previous.Dispose()
	}
	return nil
}

func (r *ultraFabricRuntime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	lease := r.lease
	r.lease = nil
	r.mu.Unlock()
	if lease != nil {
		_ = lease.Dispose()
	}
	return r.sandbox.Close()
}

var _ fabricRuntime = (*ultraFabricRuntime)(nil)
