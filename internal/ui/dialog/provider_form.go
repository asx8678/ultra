package dialog

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/asx8678/ultra/internal/config"
)

func (m *Providers) handleFormKey(msg tea.KeyPressMsg) Action {
	switch {
	case key.Matches(msg, m.keyMap.Back):
		m.clearDraftSecret()
		m.view = providerListView
		m.err = nil
		return nil
	case key.Matches(msg, m.keyMap.Previous):
		previous := (m.activeField + 3) % 4
		if m.editing && previous == 1 {
			previous = 0
		}
		m.focusFormField(previous)
		return nil
	case key.Matches(msg, m.keyMap.Next):
		next := (m.activeField + 1) % 4
		if m.editing && next == 1 {
			next = 2
		}
		m.focusFormField(next)
		return nil
	case key.Matches(msg, m.keyMap.Discover) && m.activeField == 3:
		draft := m.formDraft()
		m.loading = true
		m.err = nil
		return ActionCmd{Cmd: m.discoverCmd(draft, false)}
	default:
		return m.updateActiveInput(msg)
	}
}

func (m *Providers) updateActiveInput(msg tea.Msg) Action {
	var cmd tea.Cmd
	oldName := m.nameInput.Value()
	switch m.activeField {
	case 0:
		m.nameInput, cmd = m.nameInput.Update(msg)
		if !m.idTouched && m.nameInput.Value() != oldName {
			m.idInput.SetValue(providerSlug(m.nameInput.Value()))
		}
	case 1:
		if !m.editing {
			old := m.idInput.Value()
			m.idInput, cmd = m.idInput.Update(msg)
			m.idTouched = m.idTouched || m.idInput.Value() != old
		}
	case 2:
		m.urlInput, cmd = m.urlInput.Update(msg)
	case 3:
		m.keyInput, cmd = m.keyInput.Update(msg)
	}
	return ActionCmd{Cmd: cmd}
}

func (m *Providers) beginAdd() {
	m.editing = false
	m.idTouched = false
	m.draft = config.CustomProviderDraft{Type: catwalk.TypeOpenAICompat}
	m.nameInput.SetValue("")
	m.idInput.SetValue("")
	m.urlInput.SetValue("")
	m.keyInput.SetValue("")
	m.keyInput.Placeholder = "Enter API key (optional for local providers)"
	m.manualInput.SetValue("")
	m.models = nil
	m.modelChecked = make(map[string]bool)
	m.err = nil
	m.view = providerFormView
	m.focusFormField(0)
}

func (m *Providers) beginEdit(provider config.CustomProviderSummary) {
	m.editing = true
	m.idTouched = true
	m.draft = summaryDraft(provider)
	m.nameInput.SetValue(provider.Name)
	m.idInput.SetValue(provider.ID)
	m.urlInput.SetValue(provider.BaseURL)
	m.keyInput.SetValue("")
	m.keyInput.Placeholder = "Leave blank to keep the saved key"
	m.manualInput.SetValue("")
	m.models = slices.Clone(provider.Models)
	m.modelChecked = make(map[string]bool, len(provider.Models))
	for _, model := range provider.Models {
		m.modelChecked[model.ID] = true
	}
	m.err = nil
	m.view = providerFormView
	m.focusFormField(0)
}

func summaryDraft(provider config.CustomProviderSummary) config.CustomProviderDraft {
	return config.CustomProviderDraft{
		ID:                 provider.ID,
		Name:               provider.Name,
		Type:               catwalk.TypeOpenAICompat,
		BaseURL:            provider.BaseURL,
		AutoDiscoverModels: provider.AutoDiscoverModels,
		Models:             slices.Clone(provider.Models),
		Disabled:           provider.Disabled,
	}
}

func (m *Providers) formDraft() config.CustomProviderDraft {
	draft := m.draft
	draft.ID = strings.TrimSpace(m.idInput.Value())
	draft.Name = strings.TrimSpace(m.nameInput.Value())
	draft.Type = catwalk.TypeOpenAICompat
	draft.BaseURL = strings.TrimSpace(m.urlInput.Value())
	if key := m.keyInput.Value(); key != "" {
		draft.APIKey = &key
	} else {
		draft.APIKey = nil
	}
	return draft
}

func providerSlug(name string) string {
	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			separator = false
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '-', r == '_':
			separator = true
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func (m *Providers) focusFormField(index int) {
	m.activeField = index
	inputs := []*textinput.Model{&m.nameInput, &m.idInput, &m.urlInput, &m.keyInput}
	for i, input := range inputs {
		if i == index {
			input.Focus()
		} else {
			input.Blur()
		}
	}
}

func (m *Providers) clearDraftSecret() {
	m.keyInput.SetValue("")
	m.draft.APIKey = nil
}

func (m *Providers) saveCmd(draft config.CustomProviderDraft) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		provider, err := m.com.Workspace.SaveCustomProvider(ctx, draft)
		return providerSavedMsg{provider: provider, err: err}
	}
}

func (m *Providers) renderForm(width int) string {
	narrow := width < 40
	inputWidth := max(1, width-22)
	if narrow {
		inputWidth = max(1, width-2)
	}
	for _, input := range []*textinput.Model{&m.nameInput, &m.idInput, &m.urlInput, &m.keyInput} {
		input.SetWidth(inputWidth)
	}
	labels := []string{"Provider name", "Provider ID", "API base URL", "API key"}
	inputs := []textinput.Model{m.nameInput, m.idInput, m.urlInput, m.keyInput}
	lines := []string{m.com.Styles.Dialog.SecondaryText.Render("Protocol: OpenAI-compatible")}
	for i, input := range inputs {
		label := labels[i]
		if i == m.activeField {
			label = "> " + label
		} else {
			label = "  " + label
		}
		if narrow {
			lines = append(lines, label, input.View())
		} else {
			lines = append(lines, fmt.Sprintf("%-18s %s", label, input.View()))
		}
	}
	if m.editing {
		lines = append(lines, "", m.com.Styles.Dialog.SecondaryText.Render("Leave API key blank to keep the saved credential."))
	}
	lines = append(lines, "", m.com.Styles.Dialog.PrimaryText.Render("Press Enter from API key to test and load models."))
	return strings.Join(lines, "\n")
}
