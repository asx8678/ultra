package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asx8678/ultra/internal/lock"
	"github.com/stretchr/testify/require"
)

func TestPruneAgentRunsPreservesActiveAndRecentRuns(t *testing.T) {
	dir := t.TempDir()
	manager := newAgentOrchestrator(dir)
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	require.NoError(t, manager.persist(AgentRunSnapshot{
		RunID: "old", State: AgentRunSucceeded, StartedAt: old.Add(-time.Hour), FinishedAt: &old,
	}))
	require.NoError(t, manager.persist(AgentRunSnapshot{
		RunID: "recent", State: AgentRunSucceeded, StartedAt: now.Add(-time.Hour), FinishedAt: &now,
	}))
	require.NoError(t, manager.persist(AgentRunSnapshot{
		RunID: "active", State: AgentRunRunning, StartedAt: old,
	}))
	require.NoError(t, manager.Close(t.Context()))

	result, err := PruneAgentRuns(dir, 24*time.Hour, 100, false)
	require.NoError(t, err)
	require.Equal(t, 3, result.Scanned)
	require.Equal(t, 1, result.Removed)
	require.NoFileExists(t, dir+"/old.json")
	require.FileExists(t, dir+"/recent.json")
	require.FileExists(t, dir+"/active.json")
}

func TestPruneAgentRunsRejectsUnrelatedAndMismatchedJSON(t *testing.T) {
	dir := t.TempDir()
	finished := time.Now().UTC().Add(-48 * time.Hour)
	manager := newAgentOrchestrator(dir)
	require.NoError(t, manager.persist(AgentRunSnapshot{
		RunID: "owned", State: AgentRunSucceeded, StartedAt: finished.Add(-time.Hour), FinishedAt: &finished,
	}))
	require.NoError(t, os.Rename(filepath.Join(dir, "owned.json"), filepath.Join(dir, "mismatch.json")))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "not-a-run.json"),
		[]byte(`{"state":"succeeded","finished_at":"2020-01-01T00:00:00Z"}`),
		0o600,
	))
	require.NoError(t, manager.Close(t.Context()))

	result, err := PruneAgentRuns(dir, time.Hour, 0, false)
	require.NoError(t, err)
	require.Zero(t, result.Removed)
	require.FileExists(t, filepath.Join(dir, "not-a-run.json"))
	require.FileExists(t, filepath.Join(dir, "mismatch.json"))
}

func TestPruneAgentRunsRejectsLiveWriter(t *testing.T) {
	dir := t.TempDir()
	manager := newAgentOrchestrator(dir)
	t.Cleanup(func() { require.NoError(t, manager.Close(t.Context())) })
	_, err := PruneAgentRuns(dir, 0, 100, true)
	require.ErrorIs(t, err, lock.ErrContended)
}

func TestPruneAgentRunsValidatesRanges(t *testing.T) {
	_, err := PruneAgentRuns(t.TempDir(), -time.Second, 1, true)
	require.ErrorContains(t, err, "cannot be negative")
	_, err = PruneAgentRuns(t.TempDir(), 0, -2, true)
	require.ErrorContains(t, err, "must be -1 or greater")
}

func TestPruneAgentRunsDryRunAndCountLimit(t *testing.T) {
	dir := t.TempDir()
	manager := newAgentOrchestrator(dir)
	for index, id := range []string{"one", "two"} {
		finished := time.Now().UTC().Add(time.Duration(index) * time.Minute)
		require.NoError(t, manager.persist(AgentRunSnapshot{
			RunID: id, State: AgentRunSucceeded, StartedAt: finished, FinishedAt: &finished,
		}))
	}
	require.NoError(t, manager.Close(t.Context()))
	result, err := PruneAgentRuns(dir, 0, 1, true)
	require.NoError(t, err)
	require.Equal(t, 1, result.Removed)
	require.FileExists(t, dir+"/one.json")
	require.FileExists(t, dir+"/two.json")
}
