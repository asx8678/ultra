package repograph

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIndexedCandidateFitsRepositoryBounds(t *testing.T) {
	t.Parallel()
	require.True(t, indexedCandidateFits(0, 0, maxIndexedFileSize))
	require.True(t, indexedCandidateFits(maxIndexedFiles-1, maxIndexedSourceBytes-maxIndexedFileSize, maxIndexedFileSize))
	require.False(t, indexedCandidateFits(maxIndexedFiles, 0, 1))
	require.False(t, indexedCandidateFits(0, maxIndexedSourceBytes, 1))
	require.False(t, indexedCandidateFits(0, 0, -1))
}
