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
Use fabric_exec as the default execution path for complex coding work. Prefer it whenever a task needs two or more tool calls, parallel repository reads or searches, dependent operations, multi-file investigation or edits, or staged implementation plus verification. Compose those operations in one checked TypeScript program when practical, use Promise.all for independent calls, and set display.title to a concrete activity label.

Use a direct native tool only when the entire task is one genuinely trivial operation, such as reading one known file, making one small known edit, or running one known command. Do not wrap a single trivial call merely to use Fabric, but switch to Fabric as soon as discovery, multiple files, multiple calls, or validation is involved.

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
