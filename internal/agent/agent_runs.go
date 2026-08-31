package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/asx8678/ultra/internal/fsext"
)

// AgentRunPruneResult summarizes a run-retention pass.
type AgentRunPruneResult struct {
	Scanned int
	Removed int
	Bytes   int64
}

type pruneCandidate struct {
	path       string
	runID      string
	finishedAt time.Time
	bytes      int64
}

// PruneAgentRuns removes old valid terminal orchestration snapshots. Active,
// malformed, mismatched, unsupported, and unrelated JSON records are never
// removed. A zero maxAge disables age pruning; -1 disables count pruning.
func PruneAgentRuns(dir string, maxAge time.Duration, maxCount int, dryRun bool) (AgentRunPruneResult, error) {
	var result AgentRunPruneResult
	if maxAge < 0 {
		return result, errors.New("agent run retention age cannot be negative")
	}
	if maxCount < -1 {
		return result, errors.New("agent run retention count must be -1 or greater")
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return result, fmt.Errorf("stat agent run directory: %w", err)
	}
	release, err := acquireAgentRunMaintenanceLock(dir)
	if err != nil {
		return result, err
	}
	defer release()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result, fmt.Errorf("read agent run directory: %w", err)
	}
	candidates := make([]pruneCandidate, 0, min(len(entries), maxRetainedAgentRuns))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		result.Scanned++
		candidate, ok := readPruneCandidate(filepath.Join(dir, entry.Name()))
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	slices.SortFunc(candidates, func(a, b pruneCandidate) int {
		if order := b.finishedAt.Compare(a.finishedAt); order != 0 {
			return order
		}
		if a.runID < b.runID {
			return -1
		}
		if a.runID > b.runID {
			return 1
		}
		return 0
	})
	now := time.Now().UTC()
	mutated := false
	var pruneErr error
	for index, candidate := range candidates {
		expired := maxAge > 0 && now.Sub(candidate.finishedAt) > maxAge
		overCount := maxCount >= 0 && index >= maxCount
		if !expired && !overCount {
			continue
		}
		if !dryRun {
			// Reopen and revalidate immediately before deletion so a replaced
			// pathname cannot turn the scan into deletion of an unrelated file.
			current, ok := readPruneCandidate(candidate.path)
			if !ok || current.runID != candidate.runID || !current.finishedAt.Equal(candidate.finishedAt) {
				continue
			}
			if err := os.Remove(candidate.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				pruneErr = errors.Join(pruneErr, fmt.Errorf("remove agent run %q: %w", candidate.path, err))
				continue
			}
			mutated = true
		}
		result.Removed++
		result.Bytes += candidate.bytes
	}
	if mutated {
		pruneErr = errors.Join(pruneErr, fsext.SyncDirectory(dir))
	}
	return result, pruneErr
}

func readPruneCandidate(path string) (pruneCandidate, bool) {
	file, err := os.Open(path)
	if err != nil {
		return pruneCandidate{}, false
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxAgentSnapshotBytes {
		_ = file.Close()
		return pruneCandidate{}, false
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxAgentSnapshotBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > maxAgentSnapshotBytes {
		return pruneCandidate{}, false
	}
	var snapshot AgentRunSnapshot
	if json.Unmarshal(data, &snapshot) != nil || !validAgentRunSnapshot(snapshot, filepath.Base(path), true) {
		return pruneCandidate{}, false
	}
	return pruneCandidate{path: path, runID: snapshot.RunID, finishedAt: *snapshot.FinishedAt, bytes: info.Size()}, true
}

func validAgentRunSnapshot(snapshot AgentRunSnapshot, filename string, requireTerminal bool) bool {
	if !validAgentRunID(snapshot.RunID) || filename != snapshot.RunID+".json" {
		return false
	}
	if snapshot.SchemaVersion != 0 && snapshot.SchemaVersion != currentAgentRunSchemaVersion {
		return false
	}
	if snapshot.StartedAt.IsZero() || snapshot.Concurrency < 0 || snapshot.Concurrency > maxAgentConcurrency ||
		len(snapshot.Tasks) > maxAgentTasks || snapshot.TokenBudget < 0 || snapshot.OutputTokenBudget < 0 ||
		snapshot.InputTokensUsed < 0 || snapshot.OutputTokensUsed < 0 || snapshot.TotalTokensUsed < 0 {
		return false
	}
	if snapshot.Mode != "" && snapshot.Mode != "parallel" && snapshot.Mode != "sequential" &&
		snapshot.Mode != "graph" && snapshot.Mode != "council" {
		return false
	}
	active := snapshot.State == AgentRunQueued || snapshot.State == AgentRunRunning
	terminal := isTerminalAgentRunState(snapshot.State)
	if !active && !terminal {
		return false
	}
	if requireTerminal && (!terminal || snapshot.FinishedAt == nil) {
		return false
	}
	if terminal && snapshot.FinishedAt == nil || snapshot.FinishedAt != nil && snapshot.FinishedAt.Before(snapshot.StartedAt) {
		return false
	}
	seen := make(map[string]struct{}, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		if !validAgentTaskID(task.ID) || task.InputTokensUsed < 0 || task.OutputTokensUsed < 0 ||
			task.TotalTokensUsed < 0 || task.MaxOutputTokens < 0 || task.MaxOutputTokens > maxAgentOutputTokens {
			return false
		}
		if _, exists := seen[task.ID]; exists {
			return false
		}
		seen[task.ID] = struct{}{}
		switch task.State {
		case AgentTaskPending, AgentTaskRunning, AgentTaskSucceeded, AgentTaskFailed, AgentTaskSkipped, AgentTaskCanceled:
		default:
			return false
		}
		if terminal && (task.State == AgentTaskPending || task.State == AgentTaskRunning) {
			return false
		}
	}
	return true
}
