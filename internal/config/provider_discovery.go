package config

import (
	"context"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/asx8678/ultra/internal/discover"
)

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
