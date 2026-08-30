package fabric

import "context"

// Provider exposes schema-described actions. Implementations are trusted host
// code; guest programs can reach them only through Registry.Invoke.
type Provider interface {
	Name() string
	Description() string
	List(context.Context, ListActionsRequest, DiscoveryContext) ([]ActionDescriptor, error)
	Describe(context.Context, string, DiscoveryContext) (ActionDescriptor, bool, error)
	Invoke(context.Context, string, JSONObject, InvocationContext) (JSONValue, error)
}

// ArgumentPreparer normalizes model arguments before validation and approval.
type ArgumentPreparer interface {
	PrepareArguments(context.Context, string, JSONObject, InvocationContext) (JSONObject, error)
}

// ProviderCloser releases provider resources after every owner, view, and call
// has released its binding.
type ProviderCloser interface {
	Close() error
}

// Disposable releases a registered provider or acquired capability view.
type Disposable interface {
	Dispose() error
}

// RegisterOptions controls provider replacement.
type RegisterOptions struct {
	Replace bool
}

// ProviderBindingIdentity identifies one provider generation.
type ProviderBindingIdentity struct {
	Provider   string `json:"provider"`
	BindingID  string `json:"binding_id"`
	Generation uint64 `json:"generation"`
}

// CapabilityBinding identifies the exact descriptor and implementation pinned
// by a committed view.
type CapabilityBinding struct {
	ProviderBindingIdentity
	Ref            string `json:"ref"`
	DescriptorHash string `json:"descriptor_hash"`
}

// CatalogAction is deterministic execution-free view metadata.
type CatalogAction struct {
	Ref            string           `json:"ref"`
	Provider       string           `json:"provider"`
	Descriptor     ActionDescriptor `json:"descriptor"`
	DescriptorHash string           `json:"descriptor_hash"`
}
