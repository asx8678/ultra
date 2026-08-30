package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeGenerationChangesWithPublishedConfig(t *testing.T) {
	t.Parallel()
	store := &ConfigStore{config: &Config{}}
	before := store.RuntimeGeneration()
	store.mutateInMemory(func(*Config) {})
	require.Greater(t, store.RuntimeGeneration(), before)
}
