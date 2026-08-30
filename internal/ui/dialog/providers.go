package dialog

import (
	"cmp"
	"context"
	"fmt"
	"time"

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
