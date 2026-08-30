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

const fabricCodeModeGuidance = `<fabric_code_mode>
Code Mode is the exclusive execution path for this top-level coder. Whenever host interaction is needed, call fabric_exec; native capabilities are available only as nested Fabric actions and cannot be called directly. A response that needs no host interaction may be returned without a tool call.

For complex coding work, compose discovery, parallel repository reads or searches, dependent operations, multi-file investigation or edits, and staged implementation plus verification in one syntax-checked TypeScript program when practical. Use Promise.all for independent calls and set display.title to a concrete activity label. For one genuinely trivial operation, still call fabric_exec with one nested action rather than attempting a direct native tool. Esbuild transpiles the program without full TypeScript type checking; nested action schemas are validated authoritatively at runtime.

Fabric is available only to this top-level coder. Nested actions retain Ultra hooks, session permissions, schema validation, cancellation, budgets, and trace redaction; never use Fabric to bypass them. Return concise structured values and rely on the live Fabric card plus terminal trace for execution detail.
</fabric_code_mode>`

func addFabricCodeModeGuidance(systemPrompt string, agent config.Agent, isSubAgent bool) string {
	if isSubAgent || !slices.Contains(agent.AllowedTools, "fabric_exec") {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + fabricCodeModeGuidance
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
