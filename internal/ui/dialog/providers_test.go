package dialog

import (
	"context"
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/asx8678/ultra/internal/config"
	"github.com/asx8678/ultra/internal/ui/common"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/asx8678/ultra/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

type providerDialogWorkspace struct {
	workspace.Workspace
	providers  []config.CustomProviderSummary
	discovered []catwalk.Model
	saved      config.CustomProviderDraft
	deleted    string
}

func (w *providerDialogWorkspace) ListCustomProviders(context.Context) ([]config.CustomProviderSummary, error) {
	return w.providers, nil
}

func (w *providerDialogWorkspace) DiscoverCustomProviderModels(context.Context, config.CustomProviderDraft) ([]catwalk.Model, error) {
	return w.discovered, nil
}

func (w *providerDialogWorkspace) SaveCustomProvider(_ context.Context, draft config.CustomProviderDraft) (config.CustomProviderSummary, error) {
	w.saved = draft
	return config.CustomProviderSummary{ID: draft.ID, Name: draft.Name, Models: draft.Models, Editable: true}, nil
}

func (w *providerDialogWorkspace) DeleteCustomProvider(_ context.Context, providerID string) error {
	w.deleted = providerID
	return nil
}

func newProviderDialog(t *testing.T, ws *providerDialogWorkspace) *Providers {
	t.Helper()
	sty := styles.CharmtonePantera()
	dialog, cmd := NewProviders(&common.Common{Workspace: ws, Styles: &sty}, false)
	require.NotNil(t, cmd)
	dialog.HandleMsg(cmd().(providersLoadedMsg))
	return dialog
}

func TestProviderSlug(t *testing.T) {
	t.Parallel()
	require.Equal(t, "moonmath-zro", providerSlug("MoonMath ZRO"))
	require.Equal(t, "my-provider-2", providerSlug(" My__Provider 2 "))
}

func TestAPIKeyInputMasksCredential(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	dialog, _ := NewAPIKeyInput(
		&common.Common{Workspace: &providerDialogWorkspace{}, Styles: &sty},
		false,
		catwalk.Provider{ID: "custom", Name: "Custom"},
		config.SelectedModel{},
		config.SelectedModelTypeLarge,
	)
	dialog.input.SetValue("top-secret-value")
	require.NotContains(t, dialog.inputView(), "top-secret-value")
	require.Contains(t, dialog.inputView(), "••")
}

func TestProvidersMasksDraftAPIKey(t *testing.T) {
	t.Parallel()
	dialog := newProviderDialog(t, &providerDialogWorkspace{})
	dialog.beginAdd()
	dialog.keyInput.SetValue("top-secret-value")

	view := dialog.renderForm(70)
	require.NotContains(t, view, "top-secret-value")
	require.Contains(t, view, "••")
}

func TestProvidersEditBlankKeyMeansPreserve(t *testing.T) {
	t.Parallel()
	dialog := newProviderDialog(t, &providerDialogWorkspace{})
	dialog.beginEdit(config.CustomProviderSummary{
		ID:               "custom",
		Name:             "Custom",
		BaseURL:          "https://example.com/v1",
		Editable:         true,
		APIKeyConfigured: true,
		Models:           []catwalk.Model{{ID: "model"}},
	})

	draft := dialog.formDraft()
	require.Nil(t, draft.APIKey)
	dialog.keyInput.SetValue("replacement")
	draft = dialog.formDraft()
	require.NotNil(t, draft.APIKey)
	require.Equal(t, "replacement", *draft.APIKey)
}

func TestProvidersDiscoveryManualModelsAndSave(t *testing.T) {
	t.Parallel()
	ws := &providerDialogWorkspace{discovered: []catwalk.Model{{ID: "remote", Name: "remote"}}}
	dialog := newProviderDialog(t, ws)
	dialog.beginAdd()
	dialog.nameInput.SetValue("MoonMath ZRO")
	dialog.idInput.SetValue("moonmath-zro")
	dialog.urlInput.SetValue("https://zro.moonmath.ai/v1")
	dialog.keyInput.SetValue("replacement")

	action := dialog.discoverCmd(dialog.formDraft(), false)()
	dialog.HandleMsg(action)
	require.Equal(t, providerModelsView, dialog.view)
	dialog.manualInput.SetValue("manual-one\nmanual-two, remote")

	saveAction, ok := dialog.saveModels().(ActionCmd)
	require.True(t, ok)
	result := saveAction.Cmd()
	dialog.HandleMsg(result)

	require.Equal(t, "moonmath-zro", ws.saved.ID)
	require.Equal(t, []catwalk.Model{
		{ID: "remote", Name: "remote"},
		{ID: "manual-one", Name: "manual-one"},
		{ID: "manual-two", Name: "manual-two"},
	}, ws.saved.Models)
	require.NotNil(t, ws.saved.AutoDiscoverModels)
	require.False(t, *ws.saved.AutoDiscoverModels)
	require.Empty(t, dialog.keyInput.Value())
}

func TestProvidersDrawsAtNarrowSizes(t *testing.T) {
	t.Parallel()
	ws := &providerDialogWorkspace{providers: []config.CustomProviderSummary{{
		ID: "custom-provider-with-a-long-name", Name: "Custom Provider With A Very Long Display Name", Type: catwalk.TypeOpenAICompat, BaseURL: "https://example.com/v1", Editable: true,
	}}}
	dialog := newProviderDialog(t, ws)
	for _, size := range []image.Point{{X: 24, Y: 10}, {X: 40, Y: 14}} {
		scr := uv.NewScreenBuffer(size.X, size.Y)
		require.NotPanics(t, func() { dialog.Draw(scr, image.Rect(0, 0, size.X, size.Y)) })
		dialog.beginEdit(ws.providers[0])
		scr = uv.NewScreenBuffer(size.X, size.Y)
		require.NotPanics(t, func() { dialog.Draw(scr, image.Rect(0, 0, size.X, size.Y)) })
		dialog.view = providerModelsView
		scr = uv.NewScreenBuffer(size.X, size.Y)
		require.NotPanics(t, func() { dialog.Draw(scr, image.Rect(0, 0, size.X, size.Y)) })
		dialog.view = providerListView
	}
}

func TestProvidersDeleteRequiresConfirmation(t *testing.T) {
	t.Parallel()
	ws := &providerDialogWorkspace{providers: []config.CustomProviderSummary{{ID: "custom", Name: "Custom", Editable: true}}}
	dialog := newProviderDialog(t, ws)

	dialog.HandleMsg(tea.KeyPressMsg{Code: 'd', Text: "d"})
	require.Equal(t, providerDeleteView, dialog.view)
	require.Empty(t, ws.deleted)
	action, ok := dialog.HandleMsg(tea.KeyPressMsg{Code: 'y', Text: "y"}).(ActionCmd)
	require.True(t, ok)
	dialog.HandleMsg(action.Cmd())
	require.Equal(t, "custom", ws.deleted)
}
