package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/catwalk/pkg/catwalk"
)

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
