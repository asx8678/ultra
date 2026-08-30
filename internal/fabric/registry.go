package fabric

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/kaptinlin/jsonschema"
)

var (
	// ErrProviderExists reports a duplicate provider registration without an
	// explicit replacement request.
	ErrProviderExists = errors.New("fabric provider already registered")
	// ErrProviderNotFound reports an unknown provider.
	ErrProviderNotFound = errors.New("fabric provider not found")
	// ErrActionNotFound reports a ref outside the selected capability view.
	ErrActionNotFound = errors.New("fabric action not found")
	// ErrViewReleased reports use after capability-view release.
	ErrViewReleased = errors.New("fabric capability view released")
)

var (
	providerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	actionNamePattern   = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$-]*(?:\.[A-Za-z_$][A-Za-z0-9_$-]*)*$`)
)

type providerBinding struct {
	identity      ProviderBindingIdentity
	provider      Provider
	descriptors   map[string]ActionDescriptor
	schemas       map[string]*jsonschema.Schema
	retiring      bool
	ownerReleased bool
	owners        int
	closed        bool
}

type viewBinding struct {
	binding    *providerBinding
	descriptor ActionDescriptor
	schema     *jsonschema.Schema
	hash       string
}

type viewRecord struct {
	id             string
	runtimeDigest  string
	semanticDigest string
	bindings       map[string]viewBinding
	references     int
	released       bool
}

// Registry owns versioned providers and immutable capability views.
type Registry struct {
	mu            sync.Mutex
	current       map[string]*providerBinding
	bindings      map[string]*providerBinding
	views         map[string]*viewRecord
	generations   map[string]uint64
	activeEffects map[uint64]activeEffect
	nextView      uint64
	nextEffect    uint64
}

// CapabilityView is an immutable, closed-world lease over exact bindings.
type CapabilityView struct {
	registry *Registry
	record   *viewRecord
	once     sync.Once
}

// NewRegistry creates an empty capability registry.
func NewRegistry() *Registry {
	return &Registry{
		current:       make(map[string]*providerBinding),
		bindings:      make(map[string]*providerBinding),
		views:         make(map[string]*viewRecord),
		generations:   make(map[string]uint64),
		activeEffects: make(map[uint64]activeEffect),
	}
}

// RegisterProvider publishes a static provider catalog atomically. Provider
// implementations that change their catalog must register a replacement.
func (r *Registry) RegisterProvider(
	ctx context.Context,
	provider Provider,
	options RegisterOptions,
) (Disposable, error) {
	if provider == nil {
		return nil, errors.New("fabric provider is nil")
	}
	name := provider.Name()
	if !providerNamePattern.MatchString(name) {
		return nil, fmt.Errorf("invalid fabric provider name %q", name)
	}
	descriptors, schemas, err := snapshotProvider(ctx, provider)
	if err != nil {
		return nil, err
	}

	var closeNow []Provider
	r.mu.Lock()
	if existing := r.current[name]; existing != nil && !options.Replace {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrProviderExists, name)
	}
	generation := r.generations[name] + 1
	r.generations[name] = generation
	bindingID := fmt.Sprintf("%s:%d", name, generation)
	binding := &providerBinding{
		identity: ProviderBindingIdentity{
			Provider:   name,
			BindingID:  bindingID,
			Generation: generation,
		},
		provider:    provider,
		descriptors: descriptors,
		schemas:     schemas,
		owners:      1,
	}
	if previous := r.current[name]; previous != nil {
		previous.retiring = true
		closeNow = append(closeNow, r.releaseBindingOwnerLocked(previous)...)
	}
	r.current[name] = binding
	r.bindings[bindingID] = binding
	r.mu.Unlock()
	closeProviders(closeNow)

	return &providerLease{registry: r, binding: binding}, nil
}

// AcquireLiveView snapshots every current provider and action.
func (r *Registry) AcquireLiveView() (*CapabilityView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	bindings := make(map[string]viewBinding)
	providerNames := make([]string, 0, len(r.current))
	for name := range r.current {
		providerNames = append(providerNames, name)
	}
	slices.Sort(providerNames)
	owned := make(map[*providerBinding]struct{})
	for _, name := range providerNames {
		binding := r.current[name]
		actionNames := make([]string, 0, len(binding.descriptors))
		for action := range binding.descriptors {
			actionNames = append(actionNames, action)
		}
		slices.Sort(actionNames)
		for _, action := range actionNames {
			descriptor := cloneDescriptor(binding.descriptors[action])
			ref := name + "." + action
			bindings[ref] = viewBinding{
				binding:    binding,
				descriptor: descriptor,
				schema:     binding.schemas[action],
				hash:       descriptorHash(descriptor),
			}
			owned[binding] = struct{}{}
		}
	}
	for binding := range owned {
		binding.owners++
	}

	r.nextView++
	record := &viewRecord{
		id:             fmt.Sprintf("view:%d", r.nextView),
		runtimeDigest:  viewDigest(bindings, true),
		semanticDigest: viewDigest(bindings, false),
		bindings:       bindings,
		references:     1,
	}
	r.views[record.id] = record
	return &CapabilityView{registry: r, record: record}, nil
}

// AcquireView acquires another lease on a live committed view.
func (r *Registry) AcquireView(id string) (*CapabilityView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.views[id]
	if record == nil || record.released {
		return nil, fmt.Errorf("%w: %s", ErrViewReleased, id)
	}
	record.references++
	return &CapabilityView{registry: r, record: record}, nil
}

// ID returns the committed view identifier.
func (v *CapabilityView) ID() string {
	if v == nil || v.record == nil {
		return ""
	}
	return v.record.id
}

// RuntimeDigest includes provider binding identities and changes on equivalent
// provider replacement.
func (v *CapabilityView) RuntimeDigest() string {
	if v == nil || v.record == nil {
		return ""
	}
	return v.record.runtimeDigest
}

// SemanticDigest includes only refs and action descriptors.
func (v *CapabilityView) SemanticDigest() string {
	if v == nil || v.record == nil {
		return ""
	}
	return v.record.semanticDigest
}

// Bindings returns a deterministic copy of the committed wire identities.
func (v *CapabilityView) Bindings() []CapabilityBinding {
	if v == nil || v.record == nil {
		return nil
	}
	v.registry.mu.Lock()
	defer v.registry.mu.Unlock()
	refs := make([]string, 0, len(v.record.bindings))
	for ref := range v.record.bindings {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	result := make([]CapabilityBinding, 0, len(refs))
	for _, ref := range refs {
		binding := v.record.bindings[ref]
		result = append(result, CapabilityBinding{
			ProviderBindingIdentity: binding.binding.identity,
			Ref:                     ref,
			DescriptorHash:          binding.hash,
		})
	}
	return result
}

// Catalog returns deterministic metadata from a committed view.
func (r *Registry) Catalog(view *CapabilityView) ([]CatalogAction, error) {
	record, err := r.viewRecord(view)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(record.bindings))
	for ref := range record.bindings {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	result := make([]CatalogAction, 0, len(refs))
	for _, ref := range refs {
		binding := record.bindings[ref]
		result = append(result, CatalogAction{
			Ref:            ref,
			Provider:       binding.binding.identity.Provider,
			Descriptor:     cloneDescriptor(binding.descriptor),
			DescriptorHash: binding.hash,
		})
	}
	return result, nil
}

// Describe resolves ref only within view's closed world.
func (r *Registry) Describe(view *CapabilityView, ref string) (CatalogAction, error) {
	record, err := r.viewRecord(view)
	if err != nil {
		return CatalogAction{}, err
	}
	binding, exists := record.bindings[ref]
	if !exists {
		return CatalogAction{}, fmt.Errorf("%w: %s", ErrActionNotFound, ref)
	}
	return CatalogAction{
		Ref:            ref,
		Provider:       binding.binding.identity.Provider,
		Descriptor:     cloneDescriptor(binding.descriptor),
		DescriptorHash: binding.hash,
	}, nil
}

// Release releases this capability-view lease exactly once.
func (v *CapabilityView) Release() error {
	if v == nil || v.registry == nil || v.record == nil {
		return nil
	}
	var closeNow []Provider
	v.once.Do(func() {
		v.registry.mu.Lock()
		v.record.references--
		if v.record.references == 0 {
			v.record.released = true
			delete(v.registry.views, v.record.id)
			owned := make(map[*providerBinding]struct{})
			for _, binding := range v.record.bindings {
				owned[binding.binding] = struct{}{}
			}
			for binding := range owned {
				binding.owners--
				if binding.retiring && binding.owners == 0 {
					closeNow = append(closeNow, v.registry.retireBindingLocked(binding)...)
				}
			}
		}
		v.registry.mu.Unlock()
		closeProviders(closeNow)
	})
	return nil
}

func (r *Registry) viewRecord(view *CapabilityView) (*viewRecord, error) {
	if view == nil || view.registry != r || view.record == nil {
		return nil, ErrViewReleased
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if view.record.released || r.views[view.record.id] != view.record {
		return nil, fmt.Errorf("%w: %s", ErrViewReleased, view.record.id)
	}
	return view.record, nil
}

func (r *Registry) releaseBindingOwnerLocked(binding *providerBinding) []Provider {
	if binding.ownerReleased {
		return nil
	}
	binding.ownerReleased = true
	binding.owners--
	if binding.owners == 0 {
		return r.retireBindingLocked(binding)
	}
	return nil
}

func (r *Registry) retireBindingLocked(binding *providerBinding) []Provider {
	if binding.closed || binding.owners != 0 {
		return nil
	}
	binding.closed = true
	delete(r.bindings, binding.identity.BindingID)
	return []Provider{binding.provider}
}

type providerLease struct {
	registry *Registry
	binding  *providerBinding
	once     sync.Once
}

func (l *providerLease) Dispose() error {
	if l == nil || l.registry == nil || l.binding == nil {
		return nil
	}
	var closeNow []Provider
	l.once.Do(func() {
		l.registry.mu.Lock()
		if l.registry.current[l.binding.identity.Provider] == l.binding {
			delete(l.registry.current, l.binding.identity.Provider)
		}
		l.binding.retiring = true
		closeNow = append(closeNow, l.registry.releaseBindingOwnerLocked(l.binding)...)
		l.registry.mu.Unlock()
		closeProviders(closeNow)
	})
	return nil
}

func snapshotProvider(ctx context.Context, provider Provider) (map[string]ActionDescriptor, map[string]*jsonschema.Schema, error) {
	descriptors, err := provider.List(ctx, ListActionsRequest{Limit: 1_000}, DiscoveryContext{})
	if err != nil {
		return nil, nil, fmt.Errorf("list fabric provider %q: %w", provider.Name(), err)
	}
	result := make(map[string]ActionDescriptor, len(descriptors))
	schemas := make(map[string]*jsonschema.Schema, len(descriptors))
	compiler := jsonschema.NewCompiler()
	for _, descriptor := range descriptors {
		if !actionNamePattern.MatchString(descriptor.Name) {
			return nil, nil, fmt.Errorf("invalid fabric action name %q for provider %q", descriptor.Name, provider.Name())
		}
		if _, duplicate := result[descriptor.Name]; duplicate {
			return nil, nil, fmt.Errorf("duplicate fabric action %q for provider %q", descriptor.Name, provider.Name())
		}
		if len(descriptor.InputSchema) == 0 || !json.Valid(descriptor.InputSchema) {
			return nil, nil, fmt.Errorf("invalid input schema for %s.%s", provider.Name(), descriptor.Name)
		}
		compiled, err := compiler.Compile(descriptor.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("compile input schema for %s.%s: %w", provider.Name(), descriptor.Name, err)
		}
		result[descriptor.Name] = cloneDescriptor(descriptor)
		schemas[descriptor.Name] = compiled
	}
	return result, schemas, nil
}

func cloneDescriptor(descriptor ActionDescriptor) ActionDescriptor {
	descriptor.InputSchema = slices.Clone(descriptor.InputSchema)
	descriptor.OutputSchema = slices.Clone(descriptor.OutputSchema)
	if descriptor.Effect != nil {
		effect := *descriptor.Effect
		effect.Resources = slices.Clone(descriptor.Effect.Resources)
		descriptor.Effect = &effect
	}
	if descriptor.Annotations != nil {
		annotations := *descriptor.Annotations
		descriptor.Annotations = &annotations
	}
	return descriptor
}

func descriptorHash(descriptor ActionDescriptor) string {
	encoded, _ := json.Marshal(descriptor)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func viewDigest(bindings map[string]viewBinding, runtime bool) string {
	refs := make([]string, 0, len(bindings))
	for ref := range bindings {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	var value strings.Builder
	for _, ref := range refs {
		binding := bindings[ref]
		value.WriteString(ref)
		value.WriteByte(0)
		value.WriteString(binding.hash)
		value.WriteByte(0)
		if runtime {
			value.WriteString(binding.binding.identity.BindingID)
			value.WriteByte(0)
		}
	}
	digest := sha256.Sum256([]byte(value.String()))
	return hex.EncodeToString(digest[:])
}

func closeProviders(providers []Provider) {
	for _, provider := range providers {
		if closer, ok := provider.(ProviderCloser); ok {
			_ = closer.Close()
		}
	}
}
