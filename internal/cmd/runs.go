package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/asx8678/ultra/internal/agent"
	"github.com/spf13/cobra"
)

var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Manage durable agent orchestration runs",
	Args:  cobra.NoArgs,
}

var runsPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove old terminal agent-run snapshots",
	Long:  "Remove old terminal agent-run snapshots from local storage. Remote --host operation is not supported.",
	Args:  cobra.NoArgs,
	PreRunE: func(cmd *cobra.Command, _ []string) error {
		age, err := cmd.Flags().GetDuration("older-than")
		if err != nil {
			return err
		}
		if age < 0 {
			return errors.New("--older-than cannot be negative")
		}
		keep, err := cmd.Flags().GetInt("keep")
		if err != nil {
			return err
		}
		if keep < -1 {
			return errors.New("--keep must be -1 or greater")
		}
		if flag := cmd.Flags().Lookup("host"); flag != nil && flag.Changed {
			return errors.New("--host is not supported by runs prune; run it on the server host")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, _ []string) error {
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}
		dataDir, err := localRunDataDirectory(cmd, cwd)
		if err != nil {
			return err
		}
		age, _ := cmd.Flags().GetDuration("older-than")
		keep, _ := cmd.Flags().GetInt("keep")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		result, err := agent.PruneAgentRuns(filepath.Join(dataDir, "agents", "runs"), age, keep, dryRun)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "scanned=%d removed=%d bytes=%d dry_run=%t\n", result.Scanned, result.Removed, result.Bytes, dryRun)
		return err
	},
}

// localRunDataDirectory deliberately avoids config.Init: maintenance dry-runs
// must not execute shell configuration, migrate files, or contact providers.
func localRunDataDirectory(cmd *cobra.Command, cwd string) (string, error) {
	value, err := cmd.Flags().GetString("data-dir")
	if err != nil {
		return "", err
	}
	if value != "" {
		if !filepath.IsAbs(value) {
			value = filepath.Join(cwd, value)
		}
		return filepath.Clean(value), nil
	}
	ultraDir := filepath.Join(cwd, ".ultra")
	if _, err := os.Stat(ultraDir); err == nil || !os.IsNotExist(err) {
		return ultraDir, nil
	}
	legacyDir := filepath.Join(cwd, ".crush")
	if _, err := os.Stat(legacyDir); err == nil {
		return legacyDir, nil
	}
	return ultraDir, nil
}

func init() {
	runsPruneCmd.Flags().Duration("older-than", 30*24*time.Hour, "Prune terminal runs older than this duration")
	runsPruneCmd.Flags().Int("keep", 1000, "Maximum newest terminal runs to retain (-1 disables count pruning)")
	runsPruneCmd.Flags().Bool("dry-run", false, "Report what would be removed without deleting files")
	runsCmd.AddCommand(runsPruneCmd)
}
