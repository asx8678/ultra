package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrependStdinBounds verifies that piped input larger than
// maxStdinBytes is rejected instead of being read unbounded into memory.
func TestPrependStdinBounds(t *testing.T) {
	t.Parallel()

	over := bytes.NewReader(bytes.Repeat([]byte{'a'}, maxStdinBytes+16))
	_, err := prependStdin("prompt", over)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stdin exceeds maximum size")
}

// TestPrependStdinAppends verifies normal piped input is prepended to the
// argument prompt, preserving the previous correct concatenation semantics.
func TestPrependStdinAppends(t *testing.T) {
	t.Parallel()

	got, err := prependStdin("summarize", strings.NewReader("hello world"))
	require.NoError(t, err)
	require.Equal(t, "hello world\n\nsummarize", got)
}

// TestPrependStdinBoundary verifies input up to and including the limit is
// accepted, and that the prepended prompt is retained for exactly the limit.
func TestPrependStdinBoundary(t *testing.T) {
	t.Parallel()

	in := bytes.Repeat([]byte{'x'}, maxStdinBytes)
	got, err := prependStdin("p", bytes.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, maxStdinBytes+len("\n\np"), len(got))
	require.True(t, strings.HasPrefix(got, string(in)))
	require.True(t, strings.HasSuffix(got, "\n\np"))
}

// TestPrependStdinEmpty verifies an empty heredoc yields just the prompt.
func TestPrependStdinEmpty(t *testing.T) {
	t.Parallel()

	got, err := prependStdin("p", strings.NewReader(""))
	require.NoError(t, err)
	require.Equal(t, "\n\np", got)
}
