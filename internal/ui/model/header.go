package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/asx8678/ultra/internal/fsext"
	"github.com/asx8678/ultra/internal/session"
	"github.com/asx8678/ultra/internal/ui/common"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/asx8678/ultra/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const (
	headerDiag           = "╱"
	minHeaderDiags       = 3
	leftPadding          = 1
	rightPadding         = 1
	diagToDetailsSpacing = 1 // space between diagonal pattern and details section
)

type header struct {
	// cached logo and compact logo
	logo        string
	compactLogo string

	com     *common.Common
	width   int
	compact bool
}

// newHeader creates a new header model.
func newHeader(com *common.Common) *header {
	h := &header{
		com: com,
	}
	h.refresh()
	return h
}

// refresh rebuilds cached logo strings using the current styles. Call
// after the theme changes.
func (h *header) refresh() {
	t := h.com.Styles
	h.compactLogo = styles.ApplyBoldForegroundGrad(t.Header.LogoGradCanvas, "ULTRA", t.Header.LogoGradFromColor, t.Header.LogoGradToColor) + " "
	// Force drawHeader to re-render the wide logo on the next frame.
	h.width = 0
	h.logo = ""
}

// drawHeader draws the header for the given session. lspErrorCount comes
// from the UI's memoized LSP state: drawing runs on every frame and must not
// probe the workspace (a synchronous HTTP round-trip in client/server mode).
func (h *header) drawHeader(
	scr uv.Screen,
	area uv.Rectangle,
	session *session.Session,
	activeModel *workspace.AgentModel,
	compact bool,
	detailsOpen bool,
	width int,
	lspErrorCount int,
	hyperCredits *int,
	yolo bool,
) {
	t := h.com.Styles
	if width != h.width || compact != h.compact {
		h.logo = renderLogo(h.com.Styles, compact, width)
	}

	h.width = width
	h.compact = compact

	if !compact || session == nil {
		uv.NewStyledString(h.logo).Draw(scr, area)
		return
	}

	if session.ID == "" {
		return
	}

	var b strings.Builder
	b.WriteString(h.compactLogo)

	availDetailWidth := width - leftPadding - rightPadding - lipgloss.Width(b.String()) - minHeaderDiags - diagToDetailsSpacing
	details := renderHeaderDetails(
		h.com,
		session,
		activeModel,
		lspErrorCount,
		detailsOpen,
		availDetailWidth,
		hyperCredits,
		yolo,
	)

	remainingWidth := width -
		lipgloss.Width(b.String()) -
		lipgloss.Width(details) -
		leftPadding -
		rightPadding -
		diagToDetailsSpacing

	if remainingWidth > 0 {
		b.WriteString(t.Header.Diagonals.Render(
			strings.Repeat(headerDiag, max(minHeaderDiags, remainingWidth)),
		))
		b.WriteString(" ")
	}

	b.WriteString(details)

	view := uv.NewStyledString(
		t.Header.Wrapper.Padding(0, rightPadding, 0, leftPadding).Render(b.String()),
	)
	view.Draw(scr, area)
}

// renderHeaderDetails renders the details section of the header.
func renderHeaderDetails(
	com *common.Common,
	session *session.Session,
	activeModel *workspace.AgentModel,
	lspErrorCount int,
	detailsOpen bool,
	availWidth int,
	hyperCredits *int,
	yolo bool,
) string {
	if availWidth <= 0 {
		return ""
	}

	t := com.Styles
	mode := t.Header.ModeSafe.Render("SAFE")
	if yolo {
		mode = t.Header.ModeYolo.Render("YOLO ON")
	}
	parts := []string{mode}

	if lspErrorCount > 0 {
		parts = append(parts, t.LSP.ErrorDiagnostic.Render(fmt.Sprintf("%s%d", styles.LSPErrorIcon, lspErrorCount)))
	}

	if activeModel != nil && activeModel.CatwalkCfg.ContextWindow > 0 {
		percentage := (float64(session.CompletionTokens+session.PromptTokens) / float64(activeModel.CatwalkCfg.ContextWindow)) * 100
		percentageText := fmt.Sprintf("%d%%", int(percentage))
		if session.EstimatedUsage {
			percentageText = "~" + percentageText
		}
		parts = append(parts, t.Header.Percentage.Render(percentageText))
	}

	if com.IsHyper() && hyperCredits != nil {
		hc := t.Header.HypercreditIcon.Render(styles.HypercreditIcon) + " " + t.Header.Percentage.Render(common.FormatCredits(*hyperCredits))
		parts = append(parts, hc)
	}

	separator := t.Header.Separator.Render(" • ")
	result := strings.Join(parts, separator)
	optional := []string{renderHeaderModelIdentity(com, activeModel)}

	const dirTrimLimit = 4
	cwd := fsext.DirTrim(fsext.PrettyPath(com.Workspace.WorkingDir()), dirTrimLimit)
	optional = append(optional, t.Header.WorkingDir.Render(cwd))

	const keystroke = "ctrl+d"
	if detailsOpen {
		optional = append(optional, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" close"))
	} else {
		optional = append(optional, t.Header.Keystroke.Render(keystroke)+t.Header.KeystrokeTip.Render(" open"))
	}

	for _, part := range optional {
		if part == "" {
			continue
		}
		candidate := result + separator + part
		if ansi.StringWidth(candidate) <= availWidth {
			result = candidate
			continue
		}
		remaining := availWidth - ansi.StringWidth(result) - ansi.StringWidth(separator)
		if remaining >= 6 {
			result += separator + ansi.Truncate(part, remaining, "…")
		}
		break
	}

	return ansi.Truncate(result, availWidth, "…")
}

// renderHeaderModelIdentity renders the active, memoized model and provider.
// It never probes the workspace, keeping the draw path safe in client/server
// mode where workspace calls are synchronous HTTP requests.
func renderHeaderModelIdentity(com *common.Common, model *workspace.AgentModel) string {
	if model == nil {
		return ""
	}

	modelName := model.CatwalkCfg.Name
	if modelName == "" {
		modelName = model.ModelCfg.Model
	}
	if modelName == "" {
		return ""
	}

	providerName := model.ModelCfg.Provider
	if cfg := com.Config(); cfg != nil && cfg.Providers != nil {
		if provider, ok := cfg.Providers.Get(model.ModelCfg.Provider); ok && provider.Name != "" {
			providerName = provider.Name
		}
	}

	identity := com.Styles.Header.Model.Render(modelName)
	if providerName != "" {
		identity += com.Styles.Header.Provider.Render(" via " + providerName)
	}
	return identity
}
