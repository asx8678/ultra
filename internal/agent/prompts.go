package agent

import (
	"context"
	_ "embed"
	"slices"

	"github.com/asx8678/ultra/internal/agent/prompt"
	"github.com/asx8678/ultra/internal/config"
)

//go:embed templates/coder.md.tpl
var coderPromptTmpl []byte

//go:embed templates/task.md.tpl
var taskPromptTmpl []byte

//go:embed templates/initialize.md.tpl
var initializePromptTmpl []byte

const nativeRepoGraphGuidance = `<repository_graph>
For broad exploration in a large or unfamiliar repository, call repo_sketch before opening files. Use repo_focus for a symbol, route, file, or concept, then call repo_dwell sequentially for newly relevant semantic context; never run focus and dwell concurrently. Use repo_impact before broad refactors or reviews. Treat graph rankings as navigation evidence and verify suggested windows with exact reads and project tests.
</repository_graph>`

const fabricCodeModeGuidance = `<fabric_code_mode>
Code Mode is the execution path for ordinary host capabilities. Whenever ordinary host interaction is needed, call fabric_exec; native capabilities other than the Go agent orchestrator are available only as nested Fabric actions. The agent tool remains directly available for native Go parallel, sequential, graph, council, recursive, and background workflows; do not wrap agent orchestration in TypeScript. A response that needs no host interaction may be returned without a tool call.

For complex non-agent tool work, compose discovery, parallel repository reads or searches, dependent operations, multi-file investigation or edits, and staged implementation plus verification in one syntax-checked TypeScript program when practical. Use Promise.all for independent calls and set display.title to a concrete activity label. For one genuinely trivial operation, still call fabric_exec with one nested action rather than attempting a direct native tool. Esbuild transpiles the program without full TypeScript type checking; nested action schemas are validated authoritatively at runtime.

Fabric is available only to this top-level coder. Nested actions retain Ultra hooks, session permissions, schema validation, cancellation, budgets, and trace redaction; never use Fabric to bypass them. For repository intelligence, call host.repo_sketch before broad exploration, host.repo_focus for a semantic neighborhood, host.repo_dwell only after focus and never in the same Promise.all, and host.repo_impact before broad refactors or reviews. Return concise structured values and rely on the live Fabric card plus terminal trace for execution detail.
</fabric_code_mode>`

func addFabricCodeModeGuidance(systemPrompt string, agent config.Agent, isSubAgent bool) string {
	if !isSubAgent && slices.Contains(agent.AllowedTools, "fabric_exec") {
		return systemPrompt + "\n\n" + fabricCodeModeGuidance
	}
	for _, name := range []string{"repo_sketch", "repo_focus", "repo_dwell", "repo_impact"} {
		if !slices.Contains(agent.AllowedTools, name) {
			return systemPrompt
		}
	}
	return systemPrompt + "\n\n" + nativeRepoGraphGuidance
}

func coderPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("coder", string(coderPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func taskPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("task", string(taskPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func InitializePrompt(cfg *config.ConfigStore) (string, error) {
	systemPrompt, err := prompt.NewPrompt("initialize", string(initializePromptTmpl))
	if err != nil {
		return "", err
	}
	return systemPrompt.Build(context.Background(), "", "", cfg)
}
