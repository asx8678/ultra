package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/permission"
	"github.com/stretchr/testify/require"
)

type recordingPolicyTool struct {
	name string
	runs int
	opts fantasy.ProviderOptions
}

func (t *recordingPolicyTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: t.name}
}

func (t *recordingPolicyTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	t.runs++
	return fantasy.NewTextResponse("ran"), nil
}

func (t *recordingPolicyTool) ProviderOptions() fantasy.ProviderOptions {
	return t.opts
}

func (t *recordingPolicyTool) SetProviderOptions(options fantasy.ProviderOptions) {
	t.opts = options
}

func TestPolicyToolEnforcesEffectsCentrally(t *testing.T) {
	t.Parallel()

	permissions := permission.NewPermissionService(t.TempDir(), false, nil)
	permission.SetSessionMode(permissions, "session", permission.ModeReadOnly)
	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, "session")

	bash := &recordingPolicyTool{name: "bash"}
	response, err := (&policyTool{inner: bash, permissions: permissions}).Run(ctx, fantasy.ToolCall{Name: "bash"})
	require.NoError(t, err)
	require.Zero(t, bash.runs)
	require.Contains(t, response.Content, "blocked by read-only")

	view := &recordingPolicyTool{name: "view"}
	response, err = (&policyTool{inner: view, permissions: permissions}).Run(ctx, fantasy.ToolCall{Name: "view"})
	require.NoError(t, err)
	require.Equal(t, 1, view.runs)
	require.Equal(t, "ran", response.Content)
}
