package fabric

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateJSONRejectsCycles(t *testing.T) {
	t.Parallel()

	object := map[string]any{}
	object["self"] = object
	err := ValidateJSON(object, JSONLimits{})
	require.ErrorIs(t, err, ErrInvalidJSON)
	require.Contains(t, err.Error(), "cyclic")

	array := make([]any, 1)
	array[0] = array
	err = ValidateJSON(array, JSONLimits{})
	require.ErrorIs(t, err, ErrInvalidJSON)
}

func TestValidateJSONBoundsDepthAndBytes(t *testing.T) {
	t.Parallel()

	deep := map[string]any{"one": map[string]any{"two": true}}
	err := ValidateJSON(deep, JSONLimits{MaxDepth: 2, MaxBytes: 1024, MaxNodes: 16})
	require.ErrorIs(t, err, ErrJSONLimit)
	require.Contains(t, err.Error(), "depth")

	err = ValidateJSON(strings.Repeat("x", 16), JSONLimits{MaxDepth: 4, MaxBytes: 8, MaxNodes: 4})
	require.ErrorIs(t, err, ErrJSONLimit)
	require.Contains(t, err.Error(), "encoded bytes")

	err = ValidateJSON([]any{1, 2, 3}, JSONLimits{MaxDepth: 4, MaxBytes: 1024, MaxNodes: 3})
	require.True(t, errors.Is(err, ErrJSONLimit))
	require.Contains(t, err.Error(), "nodes")

	require.NoError(t, ValidateJSON(map[string]any{
		"null":   nil,
		"bool":   true,
		"text":   "ok",
		"number": 42,
		"array":  []any{1, "two"},
	}, JSONLimits{}))
}
