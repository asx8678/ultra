package fsext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncDirectory(t *testing.T) {
	t.Parallel()
	require.NoError(t, SyncDirectory(t.TempDir()))
}
