package config

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/asx8678/ultra/internal/csync"
	"github.com/asx8678/ultra/internal/discover"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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

// ValidateCustomProviderID validates IDs before they are interpolated into an
// sjson path.
func ValidateCustomProviderID(id string) error {
	if !providerIDPattern.MatchString(id) {
		return fmt.Errorf("%w: provider ID must match %s", ErrInvalidCustomProvider, providerIDPattern)
	}
	return nil
}

// ValidateCustomProviderBaseURL validates and normalizes an API base URL.
// Remote plaintext endpoints are rejected; HTTP remains available for local
// model servers on localhost and loopback addresses.
func ValidateCustomProviderBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2_048 {
		return "", fmt.Errorf("%w: base URL is too long", ErrInvalidCustomProvider)
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Hostname() == "" {
		return "", fmt.Errorf("%w: base URL must be an absolute HTTP(S) URL", ErrInvalidCustomProvider)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: base URL scheme must be http or https", ErrInvalidCustomProvider)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: base URL must not contain credentials", ErrInvalidCustomProvider)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: base URL must not contain a query or fragment", ErrInvalidCustomProvider)
	}
	host := u.Hostname()
	if u.Scheme == "http" && !isLocalProviderHost(host) {
		return "", fmt.Errorf("%w: remote provider URLs must use https", ErrInvalidCustomProvider)
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return "", fmt.Errorf("%w: private and special-purpose provider addresses are not allowed", ErrInvalidCustomProvider)
	}
	if strings.EqualFold(host, "metadata.google.internal") {
		return "", fmt.Errorf("%w: metadata service addresses are not allowed", ErrInvalidCustomProvider)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLocalProviderHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizeCustomProviderDraft(draft CustomProviderDraft, requireModels bool) (CustomProviderDraft, error) {
	draft.ID = strings.TrimSpace(draft.ID)
	if err := ValidateCustomProviderID(draft.ID); err != nil {
		return draft, err
	}
	draft.Name = strings.TrimSpace(draft.Name)
	if draft.Name == "" {
		draft.Name = draft.ID
	}
	if !utf8.ValidString(draft.Name) || len(draft.Name) > 80 || strings.IndexFunc(draft.Name, unicode.IsControl) >= 0 {
		return draft, fmt.Errorf("%w: provider name is invalid", ErrInvalidCustomProvider)
	}
	baseURL, err := ValidateCustomProviderBaseURL(draft.BaseURL)
	if err != nil {
		return draft, err
	}
	draft.BaseURL = baseURL
	if draft.Type == "" {
		draft.Type = catwalk.TypeOpenAICompat
	}
	if draft.Type != catwalk.TypeOpenAICompat {
		return draft, fmt.Errorf("%w: only openai-compat providers are supported by this form", ErrInvalidCustomProvider)
	}
	if draft.APIKey != nil {
		if len(*draft.APIKey) > 16*1_024 || strings.ContainsAny(*draft.APIKey, "\r\n\x00") {
			return draft, fmt.Errorf("%w: API key is invalid", ErrInvalidCustomProvider)
		}
	}
	if draft.ExtraHeaders != nil {
		for name, value := range *draft.ExtraHeaders {
			if !headerNamePattern.MatchString(name) || len(value) > 16*1_024 || strings.ContainsAny(value, "\r\n\x00") {
				return draft, fmt.Errorf("%w: extra header %q is invalid", ErrInvalidCustomProvider, name)
			}
		}
	}
	if len(draft.Models) > maxCustomProviderModels {
		return draft, fmt.Errorf("%w: too many models", ErrInvalidCustomProvider)
	}
	seen := make(map[string]struct{}, len(draft.Models))
	models := make([]catwalk.Model, 0, len(draft.Models))
	for _, model := range draft.Models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			continue
		}
		if len(model.ID) > 512 || strings.ContainsAny(model.ID, "\r\n\x00") {
			return draft, fmt.Errorf("%w: model ID is invalid", ErrInvalidCustomProvider)
		}
		if _, ok := seen[model.ID]; ok {
			continue
		}
		seen[model.ID] = struct{}{}
		model.Name = strings.TrimSpace(model.Name)
		if model.Name == "" {
			model.Name = model.ID
		}
		models = append(models, model)
	}
	draft.Models = models
	if requireModels && len(draft.Models) == 0 {
		return draft, fmt.Errorf("%w: add at least one model", ErrInvalidCustomProvider)
	}
	return draft, nil
}

func (s *ConfigStore) isKnownProviderID(id string) bool {
	for _, provider := range s.knownProviders {
		if string(provider.ID) == id {
			return true
		}
	}
	return false
}

func readRawProviders(path string) (map[string]ProviderConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]ProviderConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read managed providers: %w", err)
	}
	var raw struct {
		Providers map[string]ProviderConfig `json:"providers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode managed providers: %w", err)
	}
	if raw.Providers == nil {
		raw.Providers = map[string]ProviderConfig{}
	}
	return raw.Providers, nil
}

func providerSummary(id string, provider ProviderConfig, source CustomProviderSource, editable, builtIn, active bool) CustomProviderSummary {
	headerNames := make([]string, 0, len(provider.ExtraHeaders))
	for name := range provider.ExtraHeaders {
		headerNames = append(headerNames, name)
	}
	slices.Sort(headerNames)
	models := slices.Clone(provider.Models)
	return CustomProviderSummary{
		ID:                 id,
		Name:               cmp.Or(provider.Name, id),
		Type:               cmp.Or(provider.Type, catwalk.TypeOpenAICompat),
		BaseURL:            provider.BaseURL,
		Models:             models,
		Disabled:           provider.Disable,
		BuiltIn:            builtIn,
		Editable:           editable,
		Active:             active,
		Source:             source,
		APIKeyConfigured:   provider.APIKey != "" || provider.OAuthToken != nil,
		AutoDiscoverModels: provider.AutoDiscoverModels,
		ExtraHeaderNames:   headerNames,
	}
}

// RedactCustomProviderSecrets returns a transport-safe config snapshot. The
// server retains built-in credentials for compatibility with existing client
// features, but custom-provider credentials and arbitrary request extensions
// never cross the process boundary.
func (c *Config) RedactCustomProviderSecrets(knownProviders []catwalk.Provider) *Config {
	if c == nil {
		return nil
	}
	redacted := c.cloneForWrite()
	if c.Providers == nil {
		return redacted
	}
	known := make(map[string]struct{}, len(knownProviders))
	for _, provider := range knownProviders {
		known[string(provider.ID)] = struct{}{}
	}
	providers := make(map[string]ProviderConfig, c.Providers.Len())
	for id, provider := range c.Providers.Seq2() {
		provider.Models = slices.Clone(provider.Models)
		if _, builtIn := known[id]; !builtIn {
			provider.APIKey = ""
			provider.APIKeyTemplate = ""
			provider.OAuthToken = nil
			provider.ExtraHeaders = nil
			provider.ExtraBody = nil
			provider.ProviderOptions = nil
			provider.ExtraParams = nil
			provider.AWSAuthRefresh = ""
		}
		providers[id] = provider
	}
	redacted.Providers = csync.NewMapFrom(providers)
	return redacted
}

// ListCustomProviders returns built-in providers and redacted custom-provider
// definitions. Definitions in Ultra's global data file are editable; providers
// supplied by shell/workspace config are visible but read-only.
func (s *ConfigStore) ListCustomProviders(_ context.Context) ([]CustomProviderSummary, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	raw, err := readRawProviders(s.globalDataPath)
	if err != nil {
		return nil, err
	}
	cfg := s.Config()
	active := map[string]ProviderConfig{}
	if cfg != nil && cfg.Providers != nil {
		active = cfg.Providers.Copy()
	}

	result := make([]CustomProviderSummary, 0, len(s.knownProviders)+len(raw)+len(active))
	seen := make(map[string]struct{})
	for _, known := range s.knownProviders {
		id := string(known.ID)
		pc, ok := active[id]
		if !ok {
			pc = ProviderConfig{ID: id, Name: known.Name, Type: known.Type, BaseURL: known.APIEndpoint, Models: known.Models}
		}
		result = append(result, providerSummary(id, pc, CustomProviderSourceBuiltIn, false, true, ok && !pc.Disable))
		seen[id] = struct{}{}
	}
	for id, pc := range raw {
		if _, ok := seen[id]; ok {
			continue
		}
		activeProvider, isActive := active[id]
		editable := ValidateCustomProviderID(id) == nil
		summary := providerSummary(id, pc, CustomProviderSourceManagedGlobal, editable, false, isActive && !pc.Disable)
		summary.Shadowed = isActive && (activeProvider.BaseURL != pc.BaseURL || activeProvider.Name != pc.Name || activeProvider.Type != cmp.Or(pc.Type, catwalk.TypeOpenAICompat))
		if summary.Shadowed {
			summary.Active = false
		}
		result = append(result, summary)
		seen[id] = struct{}{}
	}
	for id, pc := range active {
		if _, ok := seen[id]; ok {
			continue
		}
		result = append(result, providerSummary(id, pc, CustomProviderSourceExternal, false, false, !pc.Disable))
	}
	slices.SortFunc(result, func(a, b CustomProviderSummary) int {
		if a.BuiltIn != b.BuiltIn {
			if a.BuiltIn {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return result, nil
}

// DiscoverCustomProviderModels tests an OpenAI-compatible provider and returns
// its manual models merged with the IDs exposed by /models. It never persists
// the draft and resolves retained credentials only on the server side.
func (s *ConfigStore) DiscoverCustomProviderModels(ctx context.Context, draft CustomProviderDraft) ([]catwalk.Model, error) {
	normalized, err := normalizeCustomProviderDraft(draft, false)
	if err != nil {
		return nil, err
	}
	if normalized.APIKey == nil || normalized.ExtraHeaders == nil {
		raw, err := readRawProviders(s.globalDataPath)
		if err != nil {
			return nil, err
		}
		if existing, ok := raw[normalized.ID]; ok {
			if normalized.APIKey == nil {
				key := existing.APIKey
				normalized.APIKey = &key
			}
			if normalized.ExtraHeaders == nil {
				headers := existing.ExtraHeaders
				normalized.ExtraHeaders = &headers
			}
		}
	}
	var key string
	if normalized.APIKey != nil {
		key = *normalized.APIKey
	}
	var headers map[string]string
	if normalized.ExtraHeaders != nil {
		headers = *normalized.ExtraHeaders
	}
	resolver := s.Resolver()
	models, err := discover.DiscoverModels(ctx, discover.Config{
		ID:             normalized.ID,
		BaseURL:        normalized.BaseURL,
		APIKey:         key,
		ExtraHeaders:   headers,
		ExistingModels: normalized.Models,
	}, resolver)
	if err != nil {
		return nil, err
	}
	normalized.Models = models
	normalized, err = normalizeCustomProviderDraft(normalized, false)
	if err != nil {
		return nil, err
	}
	return normalized.Models, nil
}

// SaveCustomProvider validates and atomically persists a complete custom
// provider in the global machine configuration. Nil credential/header fields
// preserve their stored values during edits.
func (s *ConfigStore) SaveCustomProvider(ctx context.Context, draft CustomProviderDraft) (CustomProviderSummary, error) {
	normalized, err := normalizeCustomProviderDraft(draft, true)
	if err != nil {
		return CustomProviderSummary{}, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	raw, err := readRawProviders(s.globalDataPath)
	if err != nil {
		return CustomProviderSummary{}, err
	}
	_, managed := raw[normalized.ID]
	if !managed && s.isKnownProviderID(normalized.ID) {
		return CustomProviderSummary{}, ErrCustomProviderIDConflict
	}
	var provider ProviderConfig
	oldData, candidate, err := s.writeCustomProviderMutation(func(data []byte) ([]byte, error) {
		if current := gjson.Get(string(data), "providers."+normalized.ID); current.Exists() {
			if err := json.Unmarshal([]byte(current.Raw), &provider); err != nil {
				return nil, fmt.Errorf("decode existing custom provider: %w", err)
			}
		}
		provider.ID = normalized.ID
		provider.Name = normalized.Name
		provider.Type = catwalk.TypeOpenAICompat
		provider.BaseURL = normalized.BaseURL
		provider.Models = slices.Clone(normalized.Models)
		provider.Disable = normalized.Disabled
		provider.AutoDiscoverModels = normalized.AutoDiscoverModels
		if normalized.APIKey != nil {
			provider.APIKey = *normalized.APIKey
			provider.OAuthToken = nil
		}
		if normalized.ExtraHeaders != nil {
			provider.ExtraHeaders = make(map[string]string, len(*normalized.ExtraHeaders))
			for name, value := range *normalized.ExtraHeaders {
				provider.ExtraHeaders[name] = value
			}
		}
		value, err := sjson.Set(string(data), "providers."+normalized.ID, provider)
		return []byte(value), err
	})
	if err != nil {
		return CustomProviderSummary{}, fmt.Errorf("save custom provider: %w", err)
	}
	if err := s.activateCustomProviderMutation(ctx, oldData, candidate); err != nil {
		return CustomProviderSummary{}, err
	}
	activeProvider := provider
	active := false
	if cfg := s.Config(); cfg != nil && cfg.Providers != nil {
		if configured, ok := cfg.Providers.Get(normalized.ID); ok {
			activeProvider = configured
			active = true
		}
	}
	return providerSummary(normalized.ID, activeProvider, CustomProviderSourceManagedGlobal, true, false, active && !activeProvider.Disable), nil
}

// DeleteCustomProvider removes a managed provider and all persisted selected
// and recent-model references to it in the same atomic file write.
func (s *ConfigStore) DeleteCustomProvider(ctx context.Context, providerID string) error {
	if err := ValidateCustomProviderID(providerID); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	raw, err := readRawProviders(s.globalDataPath)
	if err != nil {
		return err
	}
	if _, ok := raw[providerID]; !ok || s.isKnownProviderID(providerID) {
		return ErrCustomProviderNotManaged
	}
	if cfg := s.Config(); cfg != nil && cfg.Providers != nil {
		if provider, active := cfg.Providers.Get(providerID); active && !provider.Disable {
			hasFallback := false
			for id, candidate := range cfg.Providers.Seq2() {
				if id != providerID && !candidate.Disable && len(candidate.Models) > 0 {
					hasFallback = true
					break
				}
			}
			if !hasFallback {
				return fmt.Errorf("%w: cannot delete the only enabled provider", ErrInvalidCustomProvider)
			}
		}
	}

	oldOverrides := mapsCloneSelectedModels(s.overrides.Models)
	for modelType, selected := range s.overrides.Models {
		if selected.Provider == providerID {
			delete(s.overrides.Models, modelType)
		}
	}
	oldData, candidate, err := s.writeCustomProviderMutation(func(data []byte) ([]byte, error) {
		value, err := sjson.Delete(string(data), "providers."+providerID)
		if err != nil {
			return nil, err
		}
		for _, modelType := range []SelectedModelType{SelectedModelTypeLarge, SelectedModelTypeSmall} {
			selectedPath := "models." + string(modelType)
			if rawSelected := gjson.Get(value, selectedPath); rawSelected.Exists() {
				var selected SelectedModel
				if json.Unmarshal([]byte(rawSelected.Raw), &selected) == nil && selected.Provider == providerID {
					value, err = sjson.Delete(value, selectedPath)
					if err != nil {
						return nil, err
					}
				}
			}
			recentPath := "recent_models." + string(modelType)
			if rawRecent := gjson.Get(value, recentPath); rawRecent.Exists() {
				var recent []SelectedModel
				if json.Unmarshal([]byte(rawRecent.Raw), &recent) != nil {
					continue
				}
				recent = slices.DeleteFunc(recent, func(model SelectedModel) bool { return model.Provider == providerID })
				if len(recent) == 0 {
					value, err = sjson.Delete(value, recentPath)
				} else {
					value, err = sjson.Set(value, recentPath, recent)
				}
				if err != nil {
					return nil, err
				}
			}
		}
		return []byte(value), nil
	})
	if err != nil {
		s.overrides.Models = oldOverrides
		return fmt.Errorf("delete custom provider: %w", err)
	}
	if err := s.activateCustomProviderMutation(ctx, oldData, candidate); err != nil {
		s.overrides.Models = oldOverrides
		if s.workingDir != "" {
			if reloadErr := s.reloadFromDiskLocked(context.Background()); reloadErr != nil {
				return errors.Join(err, fmt.Errorf("restore model overrides: %w", reloadErr))
			}
		}
		return err
	}
	return nil
}

func mapsCloneSelectedModels(src map[SelectedModelType]SelectedModel) map[SelectedModelType]SelectedModel {
	if src == nil {
		return nil
	}
	dst := make(map[SelectedModelType]SelectedModel, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (s *ConfigStore) writeCustomProviderMutation(transform func([]byte) ([]byte, error)) (oldData, candidate []byte, err error) {
	err = s.atomicWrite(ScopeGlobal, func(data []byte) ([]byte, error) {
		oldData = bytes.Clone(data)
		candidate, err = transform(data)
		return candidate, err
	})
	return oldData, candidate, err
}

func (s *ConfigStore) activateCustomProviderMutation(ctx context.Context, oldData, candidate []byte) error {
	if s.workingDir == "" {
		return nil
	}
	if err := s.reloadFromDiskLocked(ctx); err == nil {
		return nil
	} else {
		activationErr := err
		rollbackErr := s.atomicWrite(ScopeGlobal, func(current []byte) ([]byte, error) {
			if !bytes.Equal(current, candidate) {
				return nil, errors.New("configuration changed concurrently; refusing rollback")
			}
			return oldData, nil
		})
		if rollbackErr == nil {
			rollbackErr = s.reloadFromDiskLocked(context.Background())
		}
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("activate custom provider: %w", activationErr), fmt.Errorf("rollback custom provider: %w", rollbackErr))
		}
		return fmt.Errorf("activate custom provider: %w", activationErr)
	}
}
