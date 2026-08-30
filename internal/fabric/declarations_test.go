package fabric

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type declarationProvider struct{}

func (declarationProvider) Name() string        { return "host" }
func (declarationProvider) Description() string { return "declaration test" }
func (declarationProvider) List(context.Context, ListActionsRequest, DiscoveryContext) ([]ActionDescriptor, error) {
	return []ActionDescriptor{{
		Name: "read-file", Description: "Read a file", Risk: RiskRead,
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{"limit":{"type":"integer"},"path":{"type":"string"},"mode":{"enum":["text","bytes"]}},
			"required":["path"],
			"additionalProperties":false
		}`),
	}}, nil
}

func (declarationProvider) Describe(context.Context, string, DiscoveryContext) (ActionDescriptor, bool, error) {
	return ActionDescriptor{}, false, nil
}

func (declarationProvider) Invoke(context.Context, string, JSONObject, InvocationContext) (JSONValue, error) {
	return nil, nil
}

func TestDeclarationsRenderPinnedInputSchema(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	lease, err := registry.RegisterProvider(t.Context(), declarationProvider{}, RegisterOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Dispose()) })
	view, err := registry.AcquireLiveView()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, view.Release()) })

	declarations, err := registry.Declarations(view)
	require.NoError(t, err)
	require.Contains(t, declarations, `"read-file"(args: { limit?: number; mode?: "bytes" | "text"; path: string }): Promise<unknown>;`)
}
