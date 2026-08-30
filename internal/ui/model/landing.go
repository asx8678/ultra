package model

import (
	"image"

	"charm.land/lipgloss/v2"
	"github.com/asx8678/ultra/internal/ui/common"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/asx8678/ultra/internal/workspace"
	"github.com/charmbracelet/ultraviolet/layout"
	"github.com/charmbracelet/x/ansi"
)

// selectedLargeModel returns the currently selected large language model as
// memoized by the off-thread busy/agent probe (see workspace_cache.go), or
// nil when the agent isn't ready. It must never probe the workspace: it is
// called on every frame and AgentIsReady/AgentModel are synchronous HTTP
// round-trips in client/server mode.
func (m *UI) selectedLargeModel() *workspace.AgentModel {
	if m.agentReady {
		model := m.agentModel
		return &model
	}
	return nil
}

// landingView renders the Ultra workspace identity, active model, and
// LSP/MCP/skills readiness. Resource columns stack on narrow terminals instead
// of squeezing three unreadable panels into the same row.
func (m *UI) landingView() string {
	t := m.com.Styles
	width := max(0, m.layout.main.Dx())
	if width == 0 {
		return ""
	}

	brand := styles.ApplyBoldForegroundGrad(
		t.Header.LogoGradCanvas,
		"ULTRA",
		t.Header.LogoGradFromColor,
		t.Header.LogoGradToColor,
	)
	tagline := t.Landing.Tagline.Render("AI engineering, in your terminal.")
	hero := ansi.Truncate(brand+"  "+tagline, width, "…")

	workspaceLabel := t.Landing.WorkspaceLabel.Render("Workspace")
	pathWidth := max(1, width-lipgloss.Width(workspaceLabel)-2)
	cwd := common.PrettyPath(t, m.com.Workspace.WorkingDir(), pathWidth)
	workspaceLine := ansi.Truncate(workspaceLabel+"  "+cwd, width, "…")

	infoSection := lipgloss.JoinVertical(
		lipgloss.Left,
		hero,
		workspaceLine,
		"",
		m.modelInfo(width),
	)

	var remainingHeightArea image.Rectangle
	layout.Vertical(
		layout.Len(lipgloss.Height(infoSection)+1),
		layout.Fill(1),
	).Split(m.layout.main).Assign(new(image.Rectangle), &remainingHeightArea)

	content := m.landingResources(width, max(1, remainingHeightArea.Dy()))
	return lipgloss.NewStyle().
		Width(width).
		Height(max(0, m.layout.main.Dy()-1)).
		PaddingTop(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, infoSection, "", content))
}

func (m *UI) landingResources(width, height int) string {
	const (
		columnGap      = 1
		minColumnWidth = 24
	)

	if width >= 3*minColumnWidth+2*columnGap {
		available := width - 2*columnGap
		baseWidth := available / 3
		remainder := available % 3
		widths := [3]int{baseWidth, baseWidth, baseWidth}
		for i := range remainder {
			widths[i]++
		}

		lspSection := m.lspInfo(widths[0], height, false)
		mcpSection := m.mcpInfo(widths[1], height, false)
		skillsSection := m.skillsInfo(widths[2], height, false)
		return lipgloss.JoinHorizontal(lipgloss.Top, lspSection, " ", mcpSection, " ", skillsSection)
	}

	sectionHeight := max(2, (height-2)/3)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.lspInfo(width, sectionHeight, false),
		"",
		m.mcpInfo(width, sectionHeight, false),
		"",
		m.skillsInfo(width, sectionHeight, false),
	)
}
