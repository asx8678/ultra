// Package toolmeta defines the model-facing metadata for built-in tools
// without importing their executable implementations.
package toolmeta

import "slices"

// Effect describes externally observable tool behavior.
type Effect uint8

const (
	EffectRead Effect = 1 << iota
	EffectWrite
	EffectExec
	EffectNetwork
)

// Descriptor is the authoritative metadata for a built-in tool.
type Descriptor struct {
	Name           string
	Group          string
	Effects        Effect
	Interactive    bool
	SubagentSafe   bool
	TaskDefault    bool
	DefaultEnabled bool
}

var descriptors = []Descriptor{
	{Name: "agent", Group: "agent", Effects: EffectRead | EffectWrite | EffectExec | EffectNetwork, DefaultEnabled: true},
	{Name: "bash", Group: "core", Effects: EffectRead | EffectWrite | EffectExec | EffectNetwork, DefaultEnabled: true},
	{Name: "ultra_info", Group: "debug", Effects: EffectRead, DefaultEnabled: true},
	{Name: "ultra_logs", Group: "debug", Effects: EffectRead, DefaultEnabled: true},
	{Name: "job_output", Group: "jobs", Effects: EffectRead, DefaultEnabled: true},
	{Name: "job_kill", Group: "jobs", Effects: EffectExec, DefaultEnabled: true},
	{Name: "download", Group: "network", Effects: EffectWrite | EffectNetwork, SubagentSafe: true, DefaultEnabled: true},
	{Name: "edit", Group: "core", Effects: EffectWrite, SubagentSafe: true, DefaultEnabled: true},
	{Name: "multiedit", Group: "core", Effects: EffectWrite, SubagentSafe: true, DefaultEnabled: true},
	{Name: "lsp_diagnostics", Group: "lsp", Effects: EffectRead, SubagentSafe: true, DefaultEnabled: true},
	{Name: "lsp_references", Group: "lsp", Effects: EffectRead, SubagentSafe: true, DefaultEnabled: true},
	{Name: "lsp_restart", Group: "lsp", Effects: EffectExec, DefaultEnabled: true},
	{Name: "lsp_symbols", Group: "lsp", Effects: EffectRead, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "lsp_definition", Group: "lsp", Effects: EffectRead, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "lsp_call_hierarchy", Group: "lsp", Effects: EffectRead, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "lsp_rename", Group: "lsp", Effects: EffectWrite, DefaultEnabled: true},
	{Name: "lsp_replace_symbol", Group: "lsp", Effects: EffectWrite, DefaultEnabled: true},
	{Name: "fetch", Group: "network", Effects: EffectRead | EffectNetwork, SubagentSafe: true, DefaultEnabled: true},
	{Name: "agentic_fetch", Group: "network", Effects: EffectRead | EffectNetwork, SubagentSafe: true, DefaultEnabled: true},
	{Name: "glob", Group: "core", Effects: EffectRead, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "grep", Group: "core", Effects: EffectRead, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "ls", Group: "core", Effects: EffectRead, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "question", Group: "interactive", Effects: EffectRead, Interactive: true, DefaultEnabled: true},
	{Name: "sourcegraph", Group: "network", Effects: EffectRead | EffectNetwork, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "todos", Group: "session", Effects: EffectWrite, DefaultEnabled: true},
	{Name: "view", Group: "core", Effects: EffectRead, SubagentSafe: true, TaskDefault: true, DefaultEnabled: true},
	{Name: "write", Group: "core", Effects: EffectWrite, SubagentSafe: true, DefaultEnabled: true},
	{Name: "list_mcp_resources", Group: "mcp", Effects: EffectRead | EffectNetwork, DefaultEnabled: true},
	{Name: "read_mcp_resource", Group: "mcp", Effects: EffectRead | EffectNetwork, DefaultEnabled: true},
}

// All returns a copy of every built-in descriptor.
func All() []Descriptor {
	return slices.Clone(descriptors)
}

// Lookup returns metadata for name.
func Lookup(name string) (Descriptor, bool) {
	for _, descriptor := range descriptors {
		if descriptor.Name == name {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// DefaultNames returns all default-enabled built-in names.
func DefaultNames() []string {
	result := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.DefaultEnabled {
			result = append(result, descriptor.Name)
		}
	}
	return result
}

// TaskDefaultNames returns the conservative default toolset for task agents.
func TaskDefaultNames() []string {
	result := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.DefaultEnabled && descriptor.TaskDefault {
			result = append(result, descriptor.Name)
		}
	}
	return result
}
