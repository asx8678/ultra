package config

import (
	"errors"
	"regexp"

	"charm.land/catwalk/pkg/catwalk"
)

const maxCustomProviderModels = 2_000

var (
	providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

	// ErrInvalidCustomProvider reports an invalid custom-provider draft.
	ErrInvalidCustomProvider = errors.New("invalid custom provider")
	// ErrCustomProviderNotManaged reports a provider not owned by Ultra's
	// machine-local data file.
	ErrCustomProviderNotManaged = errors.New("custom provider is not managed by Ultra")
	// ErrCustomProviderIDConflict reports a collision with a built-in provider.
	ErrCustomProviderIDConflict = errors.New("custom provider ID conflicts with a built-in provider")
)

// CustomProviderSource identifies where a provider shown by the manager comes
// from. Only managed_global providers can be edited by the first version of
// the UI; shell and workspace definitions remain read-only.
type CustomProviderSource string

const (
	CustomProviderSourceBuiltIn       CustomProviderSource = "built_in"
	CustomProviderSourceManagedGlobal CustomProviderSource = "managed_global"
	CustomProviderSourceExternal      CustomProviderSource = "external"
)

// CustomProviderDraft is the write-only provider representation used by the
// management UI. APIKey is nil when an edit should retain the saved key.
type CustomProviderDraft struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Type               catwalk.Type       `json:"type"`
	BaseURL            string             `json:"base_url"`
	APIKey             *string            `json:"api_key,omitempty"`
	ExtraHeaders       *map[string]string `json:"extra_headers,omitempty"`
	AutoDiscoverModels *bool              `json:"discover_models,omitempty"`
	Models             []catwalk.Model    `json:"models,omitempty"`
	Disabled           bool               `json:"disabled,omitempty"`
}

// CustomProviderSummary is a redacted provider representation suitable for
// transport to a UI. It deliberately contains no credential or header values.
type CustomProviderSummary struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	Type               catwalk.Type         `json:"type"`
	BaseURL            string               `json:"base_url,omitempty"`
	Models             []catwalk.Model      `json:"models,omitempty"`
	Disabled           bool                 `json:"disabled,omitempty"`
	BuiltIn            bool                 `json:"built_in,omitempty"`
	Editable           bool                 `json:"editable"`
	Active             bool                 `json:"active"`
	Shadowed           bool                 `json:"shadowed,omitempty"`
	Source             CustomProviderSource `json:"source"`
	APIKeyConfigured   bool                 `json:"api_key_configured"`
	AutoDiscoverModels *bool                `json:"discover_models,omitempty"`
	ExtraHeaderNames   []string             `json:"extra_header_names,omitempty"`
}
