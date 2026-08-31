package fsext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncDirectory(t *testing.T) {
	t.Parallel()
	require.NoError(t, SyncDirectory(t.TempDir()))
}

func TestReplaceFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	require.NoError(t, os.WriteFile(source, []byte("new"), 0o600))

	require.NoError(t, ReplaceFile(source, destination))
	contents, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))

	// Replacing an existing destination must succeed on every platform.
	require.NoError(t, os.WriteFile(source, []byte("newer"), 0o600))
	require.NoError(t, ReplaceFile(source, destination))
	contents, err = os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "newer", string(contents))
}
