package dialog

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"charm.land/lipgloss/v2"
	"github.com/asx8678/ultra/internal/config"
	"github.com/asx8678/ultra/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// ProvidersID identifies the custom-provider management dialog.
const ProvidersID = "providers"

const providersDialogMaxWidth = 82

type providerView int

const (
	providerListView providerView = iota
	providerFormView
	providerModelsView
	providerDeleteView
)

type providersLoadedMsg struct {
	providers []config.CustomProviderSummary
	err       error
}

type providerModelsDiscoveredMsg struct {
	providerID string
	models     []catwalk.Model
	err        error
	forTest    bool
}

type providerSavedMsg struct {
	provider config.CustomProviderSummary
	err      error
}

type providerDeletedMsg struct {
	providerID string
	err        error
}

// Providers manages built-in and custom provider instances. Draft state stays
// in this dialog until the final save command succeeds.
type Providers struct {
	com            *common.Common
	returnToModels bool
	view           providerView
	providers      []config.CustomProviderSummary
	selected       int
	loading        bool
	err            error
	status         map[string]string

	draft       config.CustomProviderDraft
	editing     bool
	idTouched   bool
	activeField int
	nameInput   textinput.Model
	idInput     textinput.Model
	urlInput    textinput.Model
	keyInput    textinput.Model

	models       []catwalk.Model
	modelChecked map[string]bool
	modelIndex   int
	manualInput  textinput.Model
	manualFocus  bool

	deleteTarget config.CustomProviderSummary
	help         help.Model
	keyMap       struct {
		Up, Down, Select, Add, Edit, Delete, Test, Toggle, Back key.Binding
		Next, Previous, Discover, Save, Manual, Check           key.Binding
	}
}

var _ Dialog = (*Providers)(nil)

// NewProviders creates a provider manager and a command that loads its
// redacted provider listing.
func NewProviders(com *common.Common, returnToModels bool) (*Providers, tea.Cmd) {
	m := &Providers{
		com:            com,
		returnToModels: returnToModels,
		status:         make(map[string]string),
		modelChecked:   make(map[string]bool),
	}
	m.help = help.New()
	m.help.Styles = com.Styles.DialogHelpStyles()
	m.initInputs()
	m.initKeys()
	m.loading = true
	return m, m.loadProvidersCmd()
}

func (m *Providers) initInputs() {
	newInput := func(placeholder string) textinput.Model {
		input := textinput.New()
		input.SetVirtualCursor(true)
		input.Placeholder = placeholder
		input.SetStyles(m.com.Styles.TextInput)
		return input
	}
	m.nameInput = newInput("MoonMath ZRO")
	m.idInput = newInput("moonmath-zro")
	m.urlInput = newInput("https://provider.example/v1")
	m.keyInput = newInput("Leave blank to keep the saved key")
	maskTextInput(&m.keyInput)
	m.manualInput = newInput("model-one, model-two")
	m.focusFormField(0)
}

func (m *Providers) initKeys() {
	m.keyMap.Up = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", "choose"))
	m.keyMap.Down = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↑/↓", "choose"))
	m.keyMap.Select = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select"))
	m.keyMap.Add = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add"))
	m.keyMap.Edit = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit"))
	m.keyMap.Delete = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	m.keyMap.Test = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test"))
	m.keyMap.Toggle = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "enable/disable"))
	m.keyMap.Back = CloseKey
	m.keyMap.Next = key.NewBinding(key.WithKeys("tab", "down"), key.WithHelp("tab", "next field"))
	m.keyMap.Previous = key.NewBinding(key.WithKeys("shift+tab", "up"), key.WithHelp("shift+tab", "previous"))
	m.keyMap.Discover = key.NewBinding(key.WithKeys("enter", "ctrl+t"), key.WithHelp("enter", "test and load models"))
	m.keyMap.Save = key.NewBinding(key.WithKeys("enter", "ctrl+s"), key.WithHelp("enter", "save provider"))
	m.keyMap.Manual = key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manual IDs"))
	m.keyMap.Check = key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle"))
}

// ID implements Dialog.
func (*Providers) ID() string { return ProvidersID }

// HandleMsg implements Dialog.
func (m *Providers) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case providersLoadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.providers = msg.providers
			m.clampSelection()
		}
		return nil
	case providerModelsDiscoveredMsg:
		m.loading = false
		if msg.forTest {
			if msg.err != nil {
				m.status[msg.providerID] = "Failed: " + msg.err.Error()
			} else {
				m.status[msg.providerID] = fmt.Sprintf("Connected · %d models", len(msg.models))
			}
			return nil
		}
		m.err = msg.err
		m.models = msg.models
		m.modelChecked = make(map[string]bool, len(msg.models))
		for _, model := range msg.models {
			m.modelChecked[model.ID] = true
		}
		m.modelIndex = 0
		m.manualFocus = false
		m.view = providerModelsView
		return nil
	case providerSavedMsg:
		m.loading = false
		m.err = msg.err
		m.keyInput.SetValue("")
		if msg.err == nil {
			m.status[msg.provider.ID] = "Saved"
			m.view = providerListView
			m.loading = true
			return ActionCmd{Cmd: m.loadProvidersCmd()}
		}
		return nil
	case providerDeletedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			delete(m.status, msg.providerID)
			m.view = providerListView
			m.loading = true
			return ActionCmd{Cmd: m.loadProvidersCmd()}
		}
		return nil
	case tea.KeyPressMsg:
		if m.loading {
			if key.Matches(msg, m.keyMap.Back) {
				return m.backAction()
			}
			return nil
		}
		switch m.view {
		case providerListView:
			return m.handleListKey(msg)
		case providerFormView:
			return m.handleFormKey(msg)
		case providerModelsView:
			return m.handleModelsKey(msg)
		case providerDeleteView:
			return m.handleDeleteKey(msg)
		}
	case tea.PasteMsg:
		return m.updateActiveInput(msg)
	}
	return nil
}

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

func (m *Providers) handleDeleteKey(msg tea.KeyPressMsg) Action {
	switch msg.String() {
	case "y", "Y", "enter":
		m.loading = true
		m.err = nil
		return ActionCmd{Cmd: m.deleteCmd(m.deleteTarget.ID)}
	case "n", "N", "esc", "alt+esc":
		m.view = providerListView
		m.err = nil
	}
	return nil
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

func (m *Providers) saveCmd(draft config.CustomProviderDraft) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		provider, err := m.com.Workspace.SaveCustomProvider(ctx, draft)
		return providerSavedMsg{provider: provider, err: err}
	}
}

func (m *Providers) deleteCmd(providerID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := m.com.Workspace.DeleteCustomProvider(ctx, providerID)
		return providerDeletedMsg{providerID: providerID, err: err}
	}
}

// Draw implements Dialog.
func (m *Providers) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	width := max(0, min(providersDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	innerWidth := max(0, width-t.Dialog.View.GetHorizontalFrameSize())
	rc := NewRenderContext(t, width)
	rc.Title = m.title()

	var body string
	switch m.view {
	case providerListView:
		body = m.renderList(innerWidth, max(1, area.Dy()-9))
	case providerFormView:
		body = m.renderForm(innerWidth)
	case providerModelsView:
		body = m.renderModels(innerWidth, max(1, area.Dy()-12))
	case providerDeleteView:
		body = m.renderDelete(innerWidth)
	}
	rc.AddPart(body)
	if m.loading {
		rc.AddPart(t.Dialog.SecondaryText.Render("Working…"))
	}
	if m.err != nil {
		rc.AddPart(t.Dialog.TitleError.Render(ansi.Truncate(m.err.Error(), innerWidth, "…")))
	}
	rc.Help = renderDialogHelp(t, &m.help, m, innerWidth)
	DrawCenter(scr, area, rc.Render())
	return nil
}

func (m *Providers) title() string {
	switch m.view {
	case providerFormView:
		if m.editing {
			return "Edit Custom Provider"
		}
		return "Add Custom Provider"
	case providerModelsView:
		return "Models for " + cmp.Or(m.nameInput.Value(), m.idInput.Value())
	case providerDeleteView:
		return "Delete Custom Provider"
	default:
		return "Manage Providers"
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

func (m *Providers) renderDelete(width int) string {
	name := cmp.Or(m.deleteTarget.Name, m.deleteTarget.ID)
	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("Delete %s?\n\nThis removes its saved definition and recent-model references.\n\nPress y to delete or n to cancel.", name))
}

// ShortHelp implements help.KeyMap.
func (m *Providers) ShortHelp() []key.Binding {
	if m.loading {
		return []key.Binding{m.keyMap.Back}
	}
	switch m.view {
	case providerFormView:
		return []key.Binding{m.keyMap.Next, m.keyMap.Discover, m.keyMap.Back}
	case providerModelsView:
		if m.manualFocus {
			return []key.Binding{m.keyMap.Save, m.keyMap.Back}
		}
		return []key.Binding{m.keyMap.Up, m.keyMap.Check, m.keyMap.Manual, m.keyMap.Save, m.keyMap.Back}
	case providerDeleteView:
		return []key.Binding{key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "delete")), m.keyMap.Back}
	default:
		return []key.Binding{m.keyMap.Up, m.keyMap.Select, m.keyMap.Add, m.keyMap.Edit, m.keyMap.Test, m.keyMap.Toggle, m.keyMap.Delete, m.keyMap.Back}
	}
}

// FullHelp implements help.KeyMap.
func (m *Providers) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }
