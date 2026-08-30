package model

import (
	"image"
	"strings"
	"testing"

	"github.com/asx8678/ultra/internal/config"
	"github.com/asx8678/ultra/internal/csync"
	"github.com/asx8678/ultra/internal/ui/common"
	"github.com/asx8678/ultra/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

type landingWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *landingWorkspace) Config() *config.Config { return w.cfg }
func (w *landingWorkspace) WorkingDir() string     { return "/workspace/ultra" }

func newLandingTestUI() *UI {
	ws := &landingWorkspace{cfg: &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	}}
	return &UI{com: common.DefaultCommon(ws)}
}

func TestLandingResourcesRespondToWidth(t *testing.T) {
	m := newLandingTestUI()
	wide := ansi.Strip(m.landingResources(90, 12))
	wideFirstLine := strings.Split(wide, "\n")[0]
	require.Contains(t, wideFirstLine, "LSPs")
	require.Contains(t, wideFirstLine, "MCPs")
	require.Contains(t, wideFirstLine, "Skills")

	narrow := ansi.Strip(m.landingResources(48, 15))
	narrowFirstLine := strings.Split(narrow, "\n")[0]
	require.Contains(t, narrowFirstLine, "LSPs")
	require.NotContains(t, narrowFirstLine, "MCPs")
	require.Less(t, strings.Index(narrow, "LSPs"), strings.Index(narrow, "MCPs"))
	require.Less(t, strings.Index(narrow, "MCPs"), strings.Index(narrow, "Skills"))
}

func TestLandingViewCarriesUltraWorkspaceIdentity(t *testing.T) {
	m := newLandingTestUI()
	m.layout.main = image.Rect(0, 0, 60, 24)

	rendered := m.landingView()
	plain := ansi.Strip(rendered)
	require.Contains(t, plain, "ULTRA")
	require.Contains(t, plain, "AI engineering, in your terminal.")
	require.Contains(t, plain, "Workspace")
	require.Contains(t, plain, "/workspace/ultra")
	require.Contains(t, plain, "No active model")

	for _, line := range strings.Split(rendered, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 60)
	}
}
