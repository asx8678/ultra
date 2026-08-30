package toolmeta

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryIsUniqueAndComplete(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{})
	defaultCount := 0
	for _, descriptor := range All() {
		require.NotEmpty(t, descriptor.Name)
		_, duplicate := seen[descriptor.Name]
		require.False(t, duplicate, "duplicate descriptor %q", descriptor.Name)
		seen[descriptor.Name] = struct{}{}
		if descriptor.DefaultEnabled {
			defaultCount++
		}
	}
	require.Len(t, DefaultNames(), defaultCount)
}

func TestTaskDefaultsPreserveConservativeSurface(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{
		"lsp_symbols", "lsp_definition", "lsp_call_hierarchy",
		"glob", "grep", "ls", "sourcegraph", "view",
	}, TaskDefaultNames())
}
