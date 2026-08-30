package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/asx8678/ultra/internal/agent"
	"github.com/asx8678/ultra/internal/ui/anim"
	"github.com/asx8678/ultra/internal/ui/list"
	"github.com/asx8678/ultra/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
)

// MessageItemAliases exposes additional stable IDs owned by one list item.
// The chat index uses these aliases to route member and nested-tool updates to
// their presentation-only container.
type MessageItemAliases interface {
	AliasIDs() []string
}

// ToolItemResolver resolves a persisted tool-call ID within a grouped item.
type ToolItemResolver interface {
	ToolItem(string) ToolMessageItem
}

// NestedToolResolver resolves the delegated agent that owns nested tool calls.
type NestedToolResolver interface {
	NestedToolContainer(string) NestedToolContainer
}

// MutableGroupItem is a grouped list item whose render cache must be
// invalidated after one of its members mutates in place.
type MutableGroupItem interface {
	Touch()
}

// AgentForestMessageItem groups one consecutive run of sibling delegated
// agents from an assistant message without changing persisted tool identities.
type AgentForestMessageItem struct {
	*list.Versioned
	*cachedMessageItem

	id       string
	sty      *styles.Styles
	agents   []*AgentToolMessageItem
	focused  bool
	expanded bool
}

var (
	_ MessageItem        = (*AgentForestMessageItem)(nil)
	_ Animatable         = (*AgentForestMessageItem)(nil)
	_ Expandable         = (*AgentForestMessageItem)(nil)
	_ MessageItemAliases = (*AgentForestMessageItem)(nil)
	_ ToolItemResolver   = (*AgentForestMessageItem)(nil)
	_ NestedToolResolver = (*AgentForestMessageItem)(nil)
	_ MutableGroupItem   = (*AgentForestMessageItem)(nil)
)

// AgentForestID returns the stable presentation ID for one consecutive run.
func AgentForestID(messageID, firstAgentID string) string {
	return messageID + ":agent-forest:" + firstAgentID
}

// NewAgentForestMessageItem creates a forest in persisted tool-call order.
func NewAgentForestMessageItem(sty *styles.Styles, messageID string, agents []*AgentToolMessageItem) *AgentForestMessageItem {
	firstAgentID := "empty"
	if len(agents) > 0 {
		firstAgentID = agents[0].ID()
	}
	return &AgentForestMessageItem{
		Versioned:         list.NewVersioned(),
		cachedMessageItem: &cachedMessageItem{},
		id:                AgentForestID(messageID, firstAgentID),
		sty:               sty,
		agents:            agents,
	}
}

func (f *AgentForestMessageItem) ID() string { return f.id }

// AgentTools returns the ordered delegated agents owned by this forest.
func (f *AgentForestMessageItem) AgentTools() []*AgentToolMessageItem {
	return append([]*AgentToolMessageItem(nil), f.agents...)
}

// AliasIDs returns member and nested tool IDs routed to this list entry.
func (f *AgentForestMessageItem) AliasIDs() []string {
	ids := make([]string, 0, len(f.agents)*2)
	for _, child := range f.agents {
		ids = append(ids, child.ID())
		for _, nested := range child.NestedTools() {
			ids = append(ids, nested.ID())
		}
	}
	return ids
}

// ToolItem resolves one member agent by its persisted tool-call ID.
func (f *AgentForestMessageItem) ToolItem(id string) ToolMessageItem {
	for _, child := range f.agents {
		if child.ID() == id {
			return child
		}
	}
	return nil
}

// NestedToolContainer resolves the member agent that owns child-session tools.
func (f *AgentForestMessageItem) NestedToolContainer(id string) NestedToolContainer {
	for _, child := range f.agents {
		if child.ID() == id {
			return child
		}
	}
	return nil
}

// AddAgent adds a newly streamed sibling while preserving issue order.
func (f *AgentForestMessageItem) AddAgent(child *AgentToolMessageItem) {
	if child == nil || f.ToolItem(child.ID()) != nil {
		return
	}
	f.agents = append(f.agents, child)
	f.Touch()
}

// Touch invalidates the group after an in-place member mutation.
func (f *AgentForestMessageItem) Touch() {
	f.clearCache()
	f.Bump()
}

func (f *AgentForestMessageItem) Finished() bool {
	if len(f.agents) == 0 {
		return true
	}
	for _, child := range f.agents {
		if !child.Finished() {
			return false
		}
	}
	return true
}

func (f *AgentForestMessageItem) SetFocused(focused bool) {
	if f.focused == focused {
		return
	}
	f.focused = focused
	f.Touch()
}

// ToggleExpanded reveals or hides full terminal agent results.
func (f *AgentForestMessageItem) ToggleExpanded() bool {
	f.expanded = !f.expanded
	f.Touch()
	return f.expanded
}

func (f *AgentForestMessageItem) StartAnimation() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(f.agents))
	for _, child := range f.agents {
		if cmd := child.StartAnimation(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (f *AgentForestMessageItem) Animate(msg anim.StepMsg) tea.Cmd {
	for _, child := range f.agents {
		if !agentContainsID(child, msg.ID) {
			continue
		}
		f.Touch()
		return child.Animate(msg)
	}
	return nil
}

func agentContainsID(child *AgentToolMessageItem, id string) bool {
	if child.ID() == id {
		return true
	}
	for _, nested := range child.NestedTools() {
		if nested.ID() == id {
			return true
		}
	}
	return false
}

func (f *AgentForestMessageItem) RawRender(width int) string {
	innerWidth := max(1, width-MessageLeftPaddingTotal)
	if cached, _, ok := f.getCachedRender(innerWidth); ok && f.Finished() {
		return cached
	}

	running, done, failed, canceled := f.counts()
	summary := fmt.Sprintf(
		"AGENTS · %d agents · %d running · %d done · %d failed",
		len(f.agents), running, done, failed,
	)
	if canceled > 0 {
		summary += fmt.Sprintf(" · %d canceled", canceled)
	}
	lines := []string{f.sty.Tool.AgentSummary.Render(summary)}
	for i, child := range f.agents {
		last := i == len(f.agents)-1
		firstPrefix, nextPrefix := "├─ ", "│  "
		if last {
			firstPrefix, nextPrefix = "└─ ", "   "
		}
		var params agent.AgentParams
		_ = json.Unmarshal([]byte(child.ToolCall().Input), &params)
		card := renderAgentCard(
			f.sty,
			child.computeStatus(),
			fmt.Sprintf("Agent %d", i+1),
			params.Prompt,
			min(52, max(8, innerWidth-3)),
		)
		childLines := strings.Split(card, "\n")
		for _, nested := range child.NestedTools() {
			for line := range strings.SplitSeq(nested.Render(max(1, innerWidth-6)), "\n") {
				childLines = append(childLines, f.sty.Tool.AgentConnector.Render("  ↳ ")+line)
			}
		}
		if f.expanded && child.result != nil && child.result.Content != "" {
			result := toolOutputPlainContent(f.sty, child.result.Content, max(1, innerWidth-8), true)
			for line := range strings.SplitSeq(result, "\n") {
				childLines = append(childLines, f.sty.Tool.AgentConnector.Render("  result ")+line)
			}
		}
		for j, line := range childLines {
			prefix := nextPrefix
			if j == 0 {
				prefix = firstPrefix
			}
			prefix = f.sty.Tool.AgentConnector.Render(prefix)
			lines = append(lines, ansi.Truncate(prefix+line, innerWidth, "…"))
		}
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], innerWidth, "…")
	}
	out := strings.Join(lines, "\n")
	f.setCachedRender(out, innerWidth, len(lines))
	return out
}

func (f *AgentForestMessageItem) Render(width int) string {
	prefix := f.sty.Messages.ToolCallBlurred.Render()
	if f.focused {
		prefix = f.sty.Messages.ToolCallFocused.Render()
	}
	lines := strings.Split(f.RawRender(width), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (f *AgentForestMessageItem) counts() (running, done, failed, canceled int) {
	for _, child := range f.agents {
		switch child.computeStatus() {
		case ToolStatusSuccess:
			done++
		case ToolStatusError:
			failed++
		case ToolStatusCanceled:
			canceled++
		default:
			running++
		}
	}
	return running, done, failed, canceled
}

// ResolveToolMessageItem returns either a direct tool item or a grouped member.
func ResolveToolMessageItem(item MessageItem, id string) ToolMessageItem {
	if resolver, ok := item.(ToolItemResolver); ok {
		return resolver.ToolItem(id)
	}
	tool, _ := item.(ToolMessageItem)
	if tool != nil && tool.ToolCall().ID == id {
		return tool
	}
	return nil
}

// ResolveNestedToolContainer returns the delegated agent identified by id.
func ResolveNestedToolContainer(item MessageItem, id string) NestedToolContainer {
	if resolver, ok := item.(NestedToolResolver); ok {
		return resolver.NestedToolContainer(id)
	}
	container, _ := item.(NestedToolContainer)
	tool, _ := item.(ToolMessageItem)
	if container != nil && tool != nil && tool.ToolCall().ID == id {
		return container
	}
	return nil
}

// TouchGroup invalidates a grouped owner after a member update.
func TouchGroup(item MessageItem) {
	if group, ok := item.(MutableGroupItem); ok {
		group.Touch()
	}
}
