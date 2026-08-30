package jsonmerge

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerge(t *testing.T) {
	t.Parallel()

	got, err := Merge(
		[]byte(`{"scalar":"first","nested":{"left":1},"items":[1],"keep":"value"}`),
		[]byte(`{"scalar":"second","nested":{"right":2},"items":[2],"keep":null}`),
	)
	require.NoError(t, err)

	var value map[string]any
	require.NoError(t, json.Unmarshal(got, &value))
	require.Equal(t, map[string]any{
		"scalar": "second",
		"nested": map[string]any{"left": float64(1), "right": float64(2)},
		"items":  []any{float64(1), float64(2)},
		"keep":   "value",
	}, value)
}

func TestMergeRejectsTypeMismatch(t *testing.T) {
	t.Parallel()

	_, err := Merge([]byte(`{"value":1}`), []byte(`{"value":"one"}`))
	require.ErrorContains(t, err, "type mismatch")
}
