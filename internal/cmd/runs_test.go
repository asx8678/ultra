package cmd

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func newRunsPruneValidationCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Duration("older-than", 30*24*time.Hour, "")
	cmd.Flags().Int("keep", 1000, "")
	cmd.Flags().String("host", "", "")
	return cmd
}

func TestRunsPruneRejectsInvalidInputBeforeSideEffects(t *testing.T) {
	cmd := newRunsPruneValidationCommand(t)
	require.NoError(t, cmd.Flags().Set("older-than", "-1h"))
	require.ErrorContains(t, runsPruneCmd.PreRunE(cmd, nil), "cannot be negative")

	cmd = newRunsPruneValidationCommand(t)
	require.NoError(t, cmd.Flags().Set("keep", "-2"))
	require.ErrorContains(t, runsPruneCmd.PreRunE(cmd, nil), "-1 or greater")

	cmd = newRunsPruneValidationCommand(t)
	require.NoError(t, cmd.Flags().Set("host", "remote:1234"))
	require.ErrorContains(t, runsPruneCmd.PreRunE(cmd, nil), "not supported")

	require.Error(t, runsPruneCmd.Args(runsPruneCmd, []string{"unexpected"}))
}

func TestLocalRunDataDirectoryDoesNotInitializeConfig(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("data-dir", "", "")
	cwd := t.TempDir()
	dir, err := localRunDataDirectory(cmd, cwd)
	require.NoError(t, err)
	require.Equal(t, cwd+"/.ultra", dir)
}
