package dialog

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/asx8678/ultra/internal/config"
	"github.com/charmbracelet/x/ansi"
)

func (m *Providers) handleModelsKey(msg tea.KeyPressMsg) Action {
	if m.manualFocus {
		switch {
		case key.Matches(msg, m.keyMap.Back):
			m.manualFocus = false
			m.manualInput.Blur()
			return nil
		case key.Matches(msg, m.keyMap.Save):
			return m.saveModels()
		default:
			var cmd tea.Cmd
			m.manualInput, cmd = m.manualInput.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	}
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.view = providerFormView
		m.focusFormField(m.activeField)
	case key.Matches(msg, m.keyMap.Up):
		if len(m.models) > 0 {
			m.modelIndex = (m.modelIndex - 1 + len(m.models)) % len(m.models)
		}
	case key.Matches(msg, m.keyMap.Down):
		if len(m.models) > 0 {
			m.modelIndex = (m.modelIndex + 1) % len(m.models)
		}
	case key.Matches(msg, m.keyMap.Check):
		if len(m.models) > 0 {
			id := m.models[m.modelIndex].ID
			m.modelChecked[id] = !m.modelChecked[id]
		}
	case key.Matches(msg, m.keyMap.Manual):
		m.manualFocus = true
		m.manualInput.Focus()
	case key.Matches(msg, m.keyMap.Save):
		return m.saveModels()
	}
	return nil
}

func (m *Providers) saveModels() Action {
	draft := m.formDraft()
	models := make([]catwalk.Model, 0, len(m.models))
	seen := make(map[string]struct{})
	for _, model := range m.models {
		if !m.modelChecked[model.ID] {
			continue
		}
		models = append(models, model)
		seen[model.ID] = struct{}{}
	}
	for _, id := range parseManualModelIDs(m.manualInput.Value()) {
		if _, ok := seen[id]; ok {
			continue
		}
		models = append(models, catwalk.Model{ID: id, Name: id})
		seen[id] = struct{}{}
	}
	if len(models) == 0 {
		m.err = fmt.Errorf("add or select at least one model")
		return nil
	}
	discoverModels := false
	draft.AutoDiscoverModels = &discoverModels
	draft.Models = models
	m.loading = true
	m.err = nil
	return ActionCmd{Cmd: m.saveCmd(draft)}
}

func parseManualModelIDs(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if id := strings.TrimSpace(part); id != "" {
			result = append(result, id)
		}
	}
	return result
}

func (m *Providers) discoverCmd(draft config.CustomProviderDraft, forTest bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		models, err := m.com.Workspace.DiscoverCustomProviderModels(ctx, draft)
		if err != nil {
			models = slices.Clone(draft.Models)
		}
		return providerModelsDiscoveredMsg{providerID: draft.ID, models: models, err: err, forTest: forTest}
	}
}

func (m *Providers) renderModels(width, availableHeight int) string {
	lines := []string{}
	if m.err == nil {
		lines = append(lines, m.com.Styles.Dialog.PrimaryText.Render("Connected successfully"), "")
	} else {
		lines = append(lines, m.com.Styles.Dialog.SecondaryText.Render("Could not load models automatically. Add model IDs manually."), "")
	}
	maxRows := max(1, min(12, availableHeight))
	start := 0
	if m.modelIndex >= maxRows {
		start = m.modelIndex - maxRows + 1
	}
	end := min(len(m.models), start+maxRows)
	for i := start; i < end; i++ {
		model := m.models[i]
		check := "[ ]"
		if m.modelChecked[model.ID] {
			check = "[x]"
		}
		line := fmt.Sprintf("%s %s", check, ansi.Truncate(model.ID, max(1, width-5), "…"))
		style := m.com.Styles.Dialog.NormalItem
		if !m.manualFocus && i == m.modelIndex {
			style = m.com.Styles.Dialog.SelectedItem
		}
		lines = append(lines, style.Width(width).Render(line))
	}
	if len(m.models) == 0 {
		lines = append(lines, m.com.Styles.Dialog.SecondaryText.Render("No discovered models."))
	}
	manualWidth := max(1, width-20)
	if width < 36 {
		manualWidth = max(1, width-2)
	}
	m.manualInput.SetWidth(manualWidth)
	manualLabel := "  Manual IDs"
	if m.manualFocus {
		manualLabel = "> Manual IDs"
	}
	if width < 36 {
		lines = append(lines, "", manualLabel, m.manualInput.View())
	} else {
		lines = append(lines, "", fmt.Sprintf("%-16s %s", manualLabel, m.manualInput.View()))
	}
	return strings.Join(lines, "\n")
}
