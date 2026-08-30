package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/asx8678/ultra/internal/csync"
	"github.com/stretchr/testify/require"
)

func customProviderTestStore(t *testing.T) *ConfigStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ultra.json")
	return &ConfigStore{
		config: &Config{
			Models:       make(map[SelectedModelType]SelectedModel),
			RecentModels: make(map[SelectedModelType][]SelectedModel),
			Providers:    csync.NewMap[string, ProviderConfig](),
		},
		globalDataPath: path,
		resolver:       IdentityResolver(),
		overrides: RuntimeOverrides{
			Models: make(map[SelectedModelType]SelectedModel),
		},
	}
}

func validCustomProviderDraft() CustomProviderDraft {
	key := "secret-value"
	discover := false
	return CustomProviderDraft{
		ID:                 "moonmath-zro",
		Name:               "MoonMath ZRO",
		Type:               catwalk.TypeOpenAICompat,
		BaseURL:            "https://zro.moonmath.ai/v1/",
		APIKey:             &key,
		AutoDiscoverModels: &discover,
		Models: []catwalk.Model{
			{ID: "glm-5.2"},
			{ID: "glm-5.2"},
			{ID: "kimi-k3", Name: "Kimi K3"},
		},
	}
}

func TestValidateCustomProviderID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"moonmath-zro", "provider_1", "a"} {
		require.NoError(t, ValidateCustomProviderID(id), id)
	}
	for _, id := range []string{"", "MoonMath", "moon.math", "moon/math", "-moon", strings.Repeat("a", 65)} {
		require.ErrorIs(t, ValidateCustomProviderID(id), ErrInvalidCustomProvider, id)
	}
}

func TestValidateCustomProviderBaseURL(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"https://example.com/v1/", "http://localhost:11434/v1", "http://127.0.0.1:8080/v1", "http://[::1]:8080/v1"} {
		got, err := ValidateCustomProviderBaseURL(raw)
		require.NoError(t, err, raw)
		require.NotEqual(t, '/', got[len(got)-1], raw)
	}
	for _, raw := range []string{"http://example.com/v1", "ftp://example.com", "https://user:pass@example.com/v1", "https://example.com/v1?q=x", "https://example.com/v1#fragment", "https://169.254.169.254/latest", "https://10.0.0.1/v1", "https://metadata.google.internal/v1"} {
		_, err := ValidateCustomProviderBaseURL(raw)
		require.ErrorIs(t, err, ErrInvalidCustomProvider, raw)
	}
}

func TestConfigStoreSaveCustomProviderNormalizesAndPreservesKey(t *testing.T) {
	t.Parallel()
	store := customProviderTestStore(t)
	draft := validCustomProviderDraft()

	summary, err := store.SaveCustomProvider(context.Background(), draft)
	require.NoError(t, err)
	require.Equal(t, "https://zro.moonmath.ai/v1", summary.BaseURL)
	require.Equal(t, []catwalk.Model{{ID: "glm-5.2", Name: "glm-5.2"}, {ID: "kimi-k3", Name: "Kimi K3"}}, summary.Models)
	require.True(t, summary.APIKeyConfigured)

	draft.APIKey = nil
	draft.Name = "MoonMath Updated"
	_, err = store.SaveCustomProvider(context.Background(), draft)
	require.NoError(t, err)

	data, err := os.ReadFile(store.globalDataPath)
	require.NoError(t, err)
	var persisted struct {
		Providers map[string]ProviderConfig `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(data, &persisted))
	provider := persisted.Providers[draft.ID]
	require.Equal(t, "secret-value", provider.APIKey)
	require.Equal(t, "MoonMath Updated", provider.Name)
	info, err := os.Stat(store.globalDataPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestConfigStoreRejectsBuiltInIDCollision(t *testing.T) {
	t.Parallel()
	store := customProviderTestStore(t)
	store.knownProviders = []catwalk.Provider{{ID: "openai", Name: "OpenAI"}}
	draft := validCustomProviderDraft()
	draft.ID = "openai"
	_, err := store.SaveCustomProvider(context.Background(), draft)
	require.ErrorIs(t, err, ErrCustomProviderIDConflict)
	_, statErr := os.Stat(store.globalDataPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestConfigStoreListCustomProvidersRedactsSecrets(t *testing.T) {
	t.Parallel()
	store := customProviderTestStore(t)
	draft := validCustomProviderDraft()
	headers := map[string]string{"X-API-Key": "header-secret"}
	draft.ExtraHeaders = &headers
	_, err := store.SaveCustomProvider(context.Background(), draft)
	require.NoError(t, err)

	providers, err := store.ListCustomProviders(context.Background())
	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.True(t, providers[0].Editable)
	require.Equal(t, []string{"X-API-Key"}, providers[0].ExtraHeaderNames)

	encoded, err := json.Marshal(providers)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-value")
	require.NotContains(t, string(encoded), "header-secret")
}

func TestRedactCustomProviderSecrets(t *testing.T) {
	t.Parallel()
	cfg := &Config{Providers: csync.NewMapFrom(map[string]ProviderConfig{
		"builtin": {ID: "builtin", APIKey: "builtin-key"},
		"custom": {
			ID:              "custom",
			APIKey:          "custom-key",
			ExtraHeaders:    map[string]string{"Authorization": "header-secret"},
			ExtraBody:       map[string]any{"secret": "body-secret"},
			ProviderOptions: map[string]any{"secret": "option-secret"},
		},
	})}
	redacted := cfg.RedactCustomProviderSecrets([]catwalk.Provider{{ID: "builtin"}})
	builtin, ok := redacted.Providers.Get("builtin")
	require.True(t, ok)
	require.Equal(t, "builtin-key", builtin.APIKey)
	custom, ok := redacted.Providers.Get("custom")
	require.True(t, ok)
	require.Empty(t, custom.APIKey)
	require.Nil(t, custom.ExtraHeaders)
	require.Nil(t, custom.ExtraBody)
	require.Nil(t, custom.ProviderOptions)
	original, ok := cfg.Providers.Get("custom")
	require.True(t, ok)
	require.Equal(t, "custom-key", original.APIKey)
}

func TestConfigStoreDiscoverCustomProviderModels(t *testing.T) {
	t.Parallel()
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		require.Equal(t, "/v1/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"remote-model"}]}`))
	}))
	defer server.Close()

	store := customProviderTestStore(t)
	key := "discovery-secret"
	draft := CustomProviderDraft{
		ID:      "local-test",
		Name:    "Local Test",
		Type:    catwalk.TypeOpenAICompat,
		BaseURL: server.URL + "/v1",
		APIKey:  &key,
		Models:  []catwalk.Model{{ID: "manual-model", Name: "Manual"}},
	}
	models, err := store.DiscoverCustomProviderModels(context.Background(), draft)
	require.NoError(t, err)
	require.Equal(t, "Bearer discovery-secret", authorization)
	require.Equal(t, []catwalk.Model{{ID: "manual-model", Name: "Manual"}, {ID: "remote-model", Name: "remote-model"}}, models)
	_, err = os.Stat(store.globalDataPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestConfigStoreSaveCustomProviderReloadsActiveConfig(t *testing.T) {
	workDir := t.TempDir()
	configDir := t.TempDir()
	globalDataDir := t.TempDir()
	workspaceDataDir := t.TempDir()
	t.Setenv("ULTRA_GLOBAL_CONFIG", configDir)
	t.Setenv("ULTRA_GLOBAL_DATA", globalDataDir)
	resetProviderState()
	t.Cleanup(resetProviderState)
	require.NoError(t, os.WriteFile(filepath.Join(globalDataDir, "ultra.json"), []byte(`{}`), 0o600))

	store, err := Load(workDir, workspaceDataDir, false)
	require.NoError(t, err)
	draft := validCustomProviderDraft()
	summary, err := store.SaveCustomProvider(context.Background(), draft)
	require.NoError(t, err)
	require.True(t, summary.Active)
	provider, ok := store.Config().Providers.Get(draft.ID)
	require.True(t, ok)
	require.Equal(t, catwalk.TypeOpenAICompat, provider.Type)
	require.Equal(t, "https://zro.moonmath.ai/v1", provider.BaseURL)
	require.Len(t, provider.Models, 2)
}

func TestConfigStoreDeleteCustomProviderCleansReferences(t *testing.T) {
	t.Parallel()
	store := customProviderTestStore(t)
	draft := validCustomProviderDraft()
	_, err := store.SaveCustomProvider(context.Background(), draft)
	require.NoError(t, err)

	selected := SelectedModel{Provider: draft.ID, Model: "glm-5.2"}
	other := SelectedModel{Provider: "other", Model: "other-model"}
	require.NoError(t, store.SetConfigFields(ScopeGlobal, map[string]any{
		"models.large":        selected,
		"models.small":        other,
		"recent_models.large": []SelectedModel{selected, other},
		"recent_models.small": []SelectedModel{selected},
	}))
	store.overrides.Models[SelectedModelTypeLarge] = selected

	require.NoError(t, store.DeleteCustomProvider(context.Background(), draft.ID))
	data, err := os.ReadFile(store.globalDataPath)
	require.NoError(t, err)
	text := string(data)
	require.NotContains(t, text, draft.ID)
	require.Contains(t, text, "other-model")
	_, pinned := store.overrides.Models[SelectedModelTypeLarge]
	require.False(t, pinned)

	providers, err := store.ListCustomProviders(context.Background())
	require.NoError(t, err)
	require.Empty(t, providers)
}
