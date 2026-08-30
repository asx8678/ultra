package dialog

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/asx8678/ultra/internal/config"
	"github.com/charmbracelet/x/ansi"
)

func (m *Providers) handleListKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		return m.backAction()
	case key.Matches(msg, m.keyMap.Up):
		if len(m.providers) > 0 {
			m.selected = (m.selected - 1 + len(m.providers)) % len(m.providers)
		}
	case key.Matches(msg, m.keyMap.Down):
		if len(m.providers) > 0 {
			m.selected = (m.selected + 1) % len(m.providers)
		}
	case key.Matches(msg, m.keyMap.Add):
		m.beginAdd()
	case key.Matches(msg, m.keyMap.Select, m.keyMap.Edit):
		if provider, ok := m.selectedProvider(); ok && provider.Editable {
			m.beginEdit(provider)
		}
	case key.Matches(msg, m.keyMap.Delete):
		if provider, ok := m.selectedProvider(); ok && provider.Editable {
			m.deleteTarget = provider
			m.err = nil
			m.view = providerDeleteView
		}
	case key.Matches(msg, m.keyMap.Test):
		if provider, ok := m.selectedProvider(); ok && !provider.BuiltIn {
			m.loading = true
			m.status[provider.ID] = "Testing…"
			return ActionCmd{Cmd: m.discoverCmd(summaryDraft(provider), true)}
		}
	case key.Matches(msg, m.keyMap.Toggle):
		if provider, ok := m.selectedProvider(); ok && provider.Editable {
			draft := summaryDraft(provider)
			draft.Disabled = !provider.Disabled
			m.loading = true
			return ActionCmd{Cmd: m.saveCmd(draft)}
		}
	}
	return nil
}

func (m *Providers) selectedProvider() (config.CustomProviderSummary, bool) {
	if m.selected < 0 || m.selected >= len(m.providers) {
		return config.CustomProviderSummary{}, false
	}
	return m.providers[m.selected], true
}

func (m *Providers) clampSelection() {
	if len(m.providers) == 0 {
		m.selected = 0
	} else if m.selected >= len(m.providers) {
		m.selected = len(m.providers) - 1
	}
}

func (m *Providers) backAction() Action {
	m.clearDraftSecret()
	return ActionCloseProviders{ReturnToModels: m.returnToModels}
}

func (m *Providers) loadProvidersCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		providers, err := m.com.Workspace.ListCustomProviders(ctx)
		return providersLoadedMsg{providers: providers, err: err}
	}
}

func (m *Providers) renderList(width, availableHeight int) string {
	if len(m.providers) == 0 {
		return m.com.Styles.Dialog.SecondaryText.Render("No providers configured. Press a to add one.")
	}
	maxRows := max(1, min(14, availableHeight))
	start := 0
	if m.selected >= maxRows {
		start = m.selected - maxRows + 1
	}
	end := min(len(m.providers), start+maxRows)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		provider := m.providers[i]
		kind := "Custom"
		if provider.BuiltIn {
			kind = "Built-in"
		} else if !provider.Editable && provider.Source == config.CustomProviderSourceManagedGlobal {
			kind = "Invalid"
		} else if !provider.Editable {
			kind = "External"
		}
		host := ""
		if parsed, err := url.Parse(provider.BaseURL); err == nil {
			host = parsed.Hostname()
		}
		state := fmt.Sprintf("%s · %s", kind, provider.Type)
		if host != "" {
			state += " · " + host
		}
		state += fmt.Sprintf(" · %d models", len(provider.Models))
		if provider.APIKeyConfigured && !provider.BuiltIn {
			state += " · key saved"
		}
		if provider.Shadowed {
			state += " · overridden"
		} else if provider.Disabled {
			state += " · disabled"
		} else if provider.Active {
			state += " · active"
		}
		if status := m.status[provider.ID]; status != "" {
			state += " · " + status
		}
		if width <= 4 {
			name := ansi.Truncate(cmp.Or(provider.Name, provider.ID), max(0, width), "…")
			style := m.com.Styles.Dialog.NormalItem
			if i == m.selected {
				style = m.com.Styles.Dialog.SelectedItem
			}
			lines = append(lines, style.Width(max(0, width)).Render(name))
			continue
		}
		stateWidth := max(1, min(width-2, width*55/100))
		state = ansi.Truncate(state, stateWidth, "…")
		nameWidth := max(1, width-lipgloss.Width(state)-1)
		name := ansi.Truncate(cmp.Or(provider.Name, provider.ID), nameWidth, "…")
		line := name + strings.Repeat(" ", max(1, width-lipgloss.Width(name)-lipgloss.Width(state))) + state
		style := m.com.Styles.Dialog.NormalItem
		if i == m.selected {
			style = m.com.Styles.Dialog.SelectedItem
		}
		lines = append(lines, style.Width(width).Render(line))
	}
	return strings.Join(lines, "\n")
}
