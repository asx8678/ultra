package fabric

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type registryTestProvider struct {
	name        string
	description string
	actions     []ActionDescriptor
	value       string

	mu         sync.Mutex
	closeCount int
}

func (p *registryTestProvider) Name() string        { return p.name }
func (p *registryTestProvider) Description() string { return p.description }

func (p *registryTestProvider) List(
	context.Context,
	ListActionsRequest,
	DiscoveryContext,
) ([]ActionDescriptor, error) {
	return append([]ActionDescriptor(nil), p.actions...), nil
}

func (p *registryTestProvider) Describe(
	_ context.Context,
	action string,
	_ DiscoveryContext,
) (ActionDescriptor, bool, error) {
	for _, descriptor := range p.actions {
		if descriptor.Name == action {
			return descriptor, true, nil
		}
	}
	return ActionDescriptor{}, false, nil
}

func (p *registryTestProvider) Invoke(
	context.Context,
	string,
	JSONObject,
	InvocationContext,
) (JSONValue, error) {
	return p.value, nil
}

func (p *registryTestProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeCount++
	return nil
}

func (p *registryTestProvider) closes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeCount
}

func testDescriptor(name string) ActionDescriptor {
	return ActionDescriptor{
		Name:        name,
		Description: name + " action",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Risk:        RiskRead,
		Effect:      &EffectDescriptor{Kind: EffectNone, Commutative: true},
	}
}

func TestRegistryCatalogDeterministic(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	zeta := &registryTestProvider{
		name:        "zeta",
		description: "zeta provider",
		actions:     []ActionDescriptor{testDescriptor("second"), testDescriptor("first")},
	}
	alpha := &registryTestProvider{
		name:        "alpha",
		description: "alpha provider",
		actions:     []ActionDescriptor{testDescriptor("read")},
	}
	_, err := registry.RegisterProvider(t.Context(), zeta, RegisterOptions{})
	require.NoError(t, err)
	_, err = registry.RegisterProvider(t.Context(), alpha, RegisterOptions{})
	require.NoError(t, err)

	view, err := registry.AcquireLiveView()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Release()) })

	first, err := registry.Catalog(view)
	require.NoError(t, err)
	second, err := registry.Catalog(view)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, []string{"alpha.read", "zeta.first", "zeta.second"}, []string{
		first[0].Ref,
		first[1].Ref,
		first[2].Ref,
	})
	require.NotEmpty(t, view.RuntimeDigest())
	require.NotEmpty(t, view.SemanticDigest())
}

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	first := &registryTestProvider{name: "host", actions: []ActionDescriptor{testDescriptor("read")}}
	second := &registryTestProvider{name: "host", actions: []ActionDescriptor{testDescriptor("write")}}
	lease, err := registry.RegisterProvider(t.Context(), first, RegisterOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Dispose()) })

	_, err = registry.RegisterProvider(t.Context(), second, RegisterOptions{})
	require.ErrorIs(t, err, ErrProviderExists)
	require.Zero(t, first.closes())
	require.Zero(t, second.closes())
}

func TestCapabilityViewClosedWorld(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	first := &registryTestProvider{name: "host", actions: []ActionDescriptor{testDescriptor("read")}}
	_, err := registry.RegisterProvider(t.Context(), first, RegisterOptions{})
	require.NoError(t, err)
	oldView, err := registry.AcquireLiveView()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, oldView.Release()) })

	second := &registryTestProvider{
		name:    "extra",
		actions: []ActionDescriptor{testDescriptor("search")},
	}
	_, err = registry.RegisterProvider(t.Context(), second, RegisterOptions{})
	require.NoError(t, err)

	_, err = registry.Describe(oldView, "extra.search")
	require.ErrorIs(t, err, ErrActionNotFound)
	newView, err := registry.AcquireLiveView()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, newView.Release()) })
	_, err = registry.Describe(newView, "extra.search")
	require.NoError(t, err)
}

func TestCapabilityViewPinsGeneration(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	oldProvider := &registryTestProvider{
		name:    "host",
		actions: []ActionDescriptor{testDescriptor("read")},
		value:   "old",
	}
	oldLease, err := registry.RegisterProvider(t.Context(), oldProvider, RegisterOptions{})
	require.NoError(t, err)
	oldView, err := registry.AcquireLiveView()
	require.NoError(t, err)
	oldBindings := oldView.Bindings()
	require.Len(t, oldBindings, 1)
	require.Equal(t, uint64(1), oldBindings[0].Generation)

	newProvider := &registryTestProvider{
		name:    "host",
		actions: []ActionDescriptor{testDescriptor("read")},
		value:   "new",
	}
	newLease, err := registry.RegisterProvider(t.Context(), newProvider, RegisterOptions{Replace: true})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, newLease.Dispose()) })
	require.Zero(t, oldProvider.closes(), "old view still owns generation one")

	newView, err := registry.AcquireLiveView()
	require.NoError(t, err)
	newBindings := newView.Bindings()
	require.Len(t, newBindings, 1)
	require.Equal(t, uint64(2), newBindings[0].Generation)
	require.NotEqual(t, oldView.RuntimeDigest(), newView.RuntimeDigest())
	require.Equal(t, oldView.SemanticDigest(), newView.SemanticDigest())

	require.NoError(t, oldView.Release())
	require.Equal(t, 1, oldProvider.closes())
	require.NoError(t, oldLease.Dispose(), "replaced owner lease is idempotent")
	require.Equal(t, 1, oldProvider.closes())
	require.NoError(t, newView.Release())
	require.NoError(t, newLease.Dispose())
	require.Equal(t, 1, newProvider.closes())

	_, err = registry.Catalog(oldView)
	require.True(t, errors.Is(err, ErrViewReleased))
}
