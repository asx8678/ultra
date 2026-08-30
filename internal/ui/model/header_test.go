package model

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/asx8678/ultra/internal/config"
	"github.com/asx8678/ultra/internal/csync"
	"github.com/asx8678/ultra/internal/session"
	"github.com/asx8678/ultra/internal/ui/common"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/asx8678/ultra/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

type headerWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *headerWorkspace) Config() *config.Config { return w.cfg }
func (w *headerWorkspace) WorkingDir() string     { return "/workspace/ultra" }

func TestRenderHeaderDetailsShowsActiveModelAndProvider(t *testing.T) {
	t.Parallel()

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("anthropic", config.ProviderConfig{ID: "anthropic", Name: "Anthropic"})
	cfg := &config.Config{Providers: providers}
	sty := styles.CharmtonePantera()
	com := &common.Common{
		Workspace: &headerWorkspace{cfg: cfg},
		Styles:    &sty,
	}
	model := &workspace.AgentModel{
		CatwalkCfg: catwalk.Model{Name: "Claude Sonnet 4", ContextWindow: 200_000},
		ModelCfg: config.SelectedModel{
			Model:    "claude-sonnet-4",
			Provider: "anthropic",
		},
	}
	sess := &session.Session{PromptTokens: 19_000, CompletionTokens: 1_000}

	rendered := renderHeaderDetails(com, sess, model, 2, false, 120, nil, false)
	plain := ansi.Strip(rendered)

	require.Contains(t, plain, "SAFE")
	require.Contains(t, plain, "E2")
	require.Contains(t, plain, "10%")
	require.Contains(t, plain, "Claude Sonnet 4 via Anthropic")
	require.Contains(t, plain, "/workspace/ultra")
	require.LessOrEqual(t, ansi.StringWidth(rendered), 120)
}

func TestRenderHeaderDetailsStaysWithinNarrowWidths(t *testing.T) {
	t.Parallel()

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("provider", config.ProviderConfig{ID: "provider", Name: "Long Provider Name"})
	sty := styles.CharmtonePantera()
	com := &common.Common{
		Workspace: &headerWorkspace{cfg: &config.Config{Providers: providers}},
		Styles:    &sty,
	}
	model := &workspace.AgentModel{
		CatwalkCfg: catwalk.Model{Name: "A Very Long Model Name", ContextWindow: 1000},
		ModelCfg:   config.SelectedModel{Model: "model", Provider: "provider"},
	}

	for _, width := range []int{8, 16, 24, 32} {
		rendered := renderHeaderDetails(com, &session.Session{PromptTokens: 500}, model, 3, false, width, nil, true)
		require.LessOrEqualf(t, ansi.StringWidth(rendered), width, "width %d", width)
		require.Contains(t, ansi.Strip(rendered), "YOLO")
	}
}
