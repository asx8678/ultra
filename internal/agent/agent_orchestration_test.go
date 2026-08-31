package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAgentPlan(t *testing.T) {
	t.Parallel()

	t.Run("parallel budget", func(t *testing.T) {
		t.Parallel()
		plan, err := normalizeAgentPlan(AgentParams{
			Tasks:       []AgentTask{{ID: "a", Prompt: "one"}, {ID: "b", Prompt: "two"}},
			TokenBudget: 11,
		})
		require.NoError(t, err)
		require.Equal(t, "parallel", plan.Mode)
		require.Equal(t, int64(6), plan.Tasks[0].MaxOutputTokens)
		require.Equal(t, int64(5), plan.Tasks[1].MaxOutputTokens)
	})

	t.Run("sequential dependencies", func(t *testing.T) {
		t.Parallel()
		plan, err := normalizeAgentPlan(AgentParams{
			Mode:        "pipeline",
			Tasks:       []AgentTask{{ID: "a", Prompt: "one"}, {ID: "b", Prompt: "two"}, {ID: "c", Prompt: "three"}},
			Concurrency: 9,
		})
		require.NoError(t, err)
		require.Equal(t, "sequential", plan.Mode)
		require.Equal(t, 1, plan.Concurrency)
		require.Equal(t, []string{"a"}, plan.Tasks[1].DependsOn)
		require.Equal(t, []string{"b"}, plan.Tasks[2].DependsOn)
	})

	t.Run("council synthesis", func(t *testing.T) {
		t.Parallel()
		plan, err := normalizeAgentPlan(AgentParams{
			Mode:  "council",
			Tasks: []AgentTask{{ID: "security", Prompt: "review"}, {ID: "performance", Prompt: "review"}},
		})
		require.NoError(t, err)
		require.Len(t, plan.Tasks, 3)
		require.Equal(t, []string{"security", "performance"}, plan.Tasks[2].DependsOn)
	})

	t.Run("cycle rejected", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeAgentPlan(AgentParams{Mode: "graph", Tasks: []AgentTask{
			{ID: "a", Prompt: "one", DependsOn: []string{"b"}},
			{ID: "b", Prompt: "two", DependsOn: []string{"a"}},
		}})
		require.ErrorContains(t, err, "cycle")
	})
}

func TestNativeAgentOrchestrationRunsInParallel(t *testing.T) {
	coord, parentID, providerID := newOrchestrationTestCoordinator(t)
	var current atomic.Int32
	var maximum atomic.Int32
	var wrongBudget atomic.Bool
	agent := newMockAgent(providerID, 256, func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		if call.MaxOutputTokens != 4 {
			wrongBudget.Store(true)
		}
		value := current.Add(1)
		defer current.Add(-1)
		for {
			old := maximum.Load()
			if value <= old || maximum.CompareAndSwap(old, value) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		return &fantasy.AgentResult{
			Response:   fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: call.Prompt}}},
			TotalUsage: fantasy.Usage{OutputTokens: 3, TotalTokens: 3},
		}, nil
	})

	response := invokeAgentTool(t, coord.newAgentTool(agent), parentID, AgentParams{
		Tasks:       []AgentTask{{ID: "a", Prompt: "A"}, {ID: "b", Prompt: "B"}, {ID: "c", Prompt: "C"}},
		Concurrency: 2,
		TokenBudget: 12,
	})
	require.False(t, response.IsError, response.Content)
	var snapshot AgentRunSnapshot
	require.NoError(t, json.Unmarshal([]byte(response.Content), &snapshot))
	require.Equal(t, AgentRunSucceeded, snapshot.State)
	require.Equal(t, int32(2), maximum.Load())
	require.False(t, wrongBudget.Load())
	require.Equal(t, int64(12), snapshot.TokenBudget)
	require.Equal(t, int64(9), snapshot.TokensUsed)
	for _, task := range snapshot.Tasks {
		require.Equal(t, AgentTaskSucceeded, task.State)
		require.NotEmpty(t, task.SessionID)
	}
}

func TestNativeAgentSequentialDependencyHandoff(t *testing.T) {
	coord, parentID, providerID := newOrchestrationTestCoordinator(t)
	var mu sync.Mutex
	var prompts []string
	agent := newMockAgent(providerID, 256, func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		mu.Lock()
		prompts = append(prompts, call.Prompt)
		mu.Unlock()
		return agentResultWithText("result:" + call.Prompt), nil
	})

	response := invokeAgentTool(t, coord.newAgentTool(agent), parentID, AgentParams{
		Mode:  "sequential",
		Tasks: []AgentTask{{ID: "discover", Prompt: "find it"}, {ID: "implement", Prompt: "build it"}},
	})
	require.False(t, response.IsError, response.Content)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, prompts, 2)
	require.Contains(t, prompts[1], "<dependency_results>")
	require.Contains(t, prompts[1], "result:find it")
}

func TestNativeAgentBackgroundSpawnAndWait(t *testing.T) {
	coord, parentID, providerID := newOrchestrationTestCoordinator(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	agent := newMockAgent(providerID, 256, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return agentResultWithText("finished"), nil
		}
	})
	tool := coord.newAgentTool(agent)

	spawn := invokeAgentTool(t, tool, parentID, AgentParams{Action: "spawn", Prompt: "background"})
	require.False(t, spawn.IsError, spawn.Content)
	var initial AgentRunSnapshot
	require.NoError(t, json.Unmarshal([]byte(spawn.Content), &initial))
	require.NotEmpty(t, initial.RunID)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background agent did not start")
	}
	close(release)

	wait := invokeAgentTool(t, tool, parentID, AgentParams{Action: "wait", RunID: initial.RunID})
	require.False(t, wait.IsError, wait.Content)
	var completed AgentRunSnapshot
	require.NoError(t, json.Unmarshal([]byte(wait.Content), &completed))
	require.Equal(t, AgentRunSucceeded, completed.State)
	require.Equal(t, "finished", completed.Tasks[0].Output)
}

func TestNativeAgentBackgroundCancel(t *testing.T) {
	coord, parentID, providerID := newOrchestrationTestCoordinator(t)
	started := make(chan struct{})
	var once sync.Once
	agent := newMockAgent(providerID, 256, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	})
	tool := coord.newAgentTool(agent)

	spawn := invokeAgentTool(t, tool, parentID, AgentParams{Action: "spawn", Prompt: "background"})
	var initial AgentRunSnapshot
	require.NoError(t, json.Unmarshal([]byte(spawn.Content), &initial))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background agent did not start")
	}
	canceled := invokeAgentTool(t, tool, parentID, AgentParams{Action: "cancel", RunID: initial.RunID})
	require.False(t, canceled.IsError, canceled.Content)
	wait := invokeAgentTool(t, tool, parentID, AgentParams{Action: "wait", RunID: initial.RunID})
	var completed AgentRunSnapshot
	require.NoError(t, json.Unmarshal([]byte(wait.Content), &completed))
	require.Equal(t, AgentRunCanceled, completed.State)
}

func TestAgentOrchestratorRecoversPanics(t *testing.T) {
	manager := newAgentOrchestrator(t.TempDir())
	job, err := manager.Start(t.Context(), AgentRunSnapshot{
		RunID: "agent-panic", State: AgentRunQueued, ParentSessionID: "parent", StartedAt: time.Now().UTC(),
	}, normalizedAgentPlan{Mode: "parallel", Concurrency: 1}, func(context.Context, *agentRun) {
		panic("boom")
	})
	require.NoError(t, err)
	<-job.done
	snapshot := job.Snapshot()
	require.Equal(t, AgentRunFailed, snapshot.State)
	require.Contains(t, snapshot.Error, "boom")
}

func TestAgentOrchestratorCloseDrainsWorkers(t *testing.T) {
	manager := newAgentOrchestrator(t.TempDir())
	started := make(chan struct{})
	exited := make(chan struct{})
	_, err := manager.Start(t.Context(), AgentRunSnapshot{
		RunID: "agent-close", State: AgentRunRunning, ParentSessionID: "parent", StartedAt: time.Now().UTC(),
	}, normalizedAgentPlan{Mode: "parallel", Concurrency: 1}, func(ctx context.Context, _ *agentRun) {
		close(started)
		<-ctx.Done()
		close(exited)
	})
	require.NoError(t, err)
	<-started

	manager.Close()
	select {
	case <-exited:
	default:
		t.Fatal("orchestrator close returned before worker exited")
	}
}

func TestAgentOrchestratorStartRequiresDurableInitialRecord(t *testing.T) {
	dir := t.TempDir()
	manager := newAgentOrchestrator(dir)
	require.Error(t, manager.persist(AgentRunSnapshot{RunID: "../escape"}))

	path := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(path, []byte("file"), 0o600))
	manager = newAgentOrchestrator(path)
	_, err := manager.Start(t.Context(), AgentRunSnapshot{
		RunID: "agent-undurable", State: AgentRunQueued, ParentSessionID: "parent", StartedAt: time.Now().UTC(),
	}, normalizedAgentPlan{Mode: "parallel", Concurrency: 1}, func(context.Context, *agentRun) {})
	require.ErrorContains(t, err, "persist initial agent run")
}

func TestNativeAgentLegacyPromptReturnsPlainText(t *testing.T) {
	coord, parentID, providerID := newOrchestrationTestCoordinator(t)
	agent := newMockAgent(providerID, 256, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		return agentResultWithText("legacy output"), nil
	})

	response := invokeAgentTool(t, coord.newAgentTool(agent), parentID, AgentParams{Prompt: "legacy"})
	require.False(t, response.IsError)
	require.Equal(t, "legacy output", response.Content)
	legacySessionID := coord.sessions.CreateAgentToolSessionID(
		"message-orchestration",
		"call-"+strings.ReplaceAll(t.Name(), "/", "-"),
	)
	_, err := coord.sessions.Get(t.Context(), legacySessionID)
	require.NoError(t, err)
}

func TestAgentRunPersistenceMarksInterruptedWork(t *testing.T) {
	dir := t.TempDir()
	manager := newAgentOrchestrator(dir)
	require.NoError(t, manager.persist(AgentRunSnapshot{
		RunID: "agent-persisted", State: AgentRunRunning, ParentSessionID: "parent", StartedAt: time.Now().UTC(),
		Tasks: []AgentTaskResult{{ID: "running", State: AgentTaskRunning}, {ID: "pending", State: AgentTaskPending}},
	}))

	reloaded := newAgentOrchestrator(dir)
	snapshot, err := reloaded.Snapshot("agent-persisted", "parent")
	require.NoError(t, err)
	require.Equal(t, AgentRunInterrupted, snapshot.State)
	require.NotNil(t, snapshot.FinishedAt)
	for _, task := range snapshot.Tasks {
		require.Equal(t, AgentTaskCanceled, task.State)
		require.NotEmpty(t, task.Error)
		require.NotNil(t, task.FinishedAt)
	}
}

func TestNativeAgentPerWorkerToolControls(t *testing.T) {
	coord, _, _ := newOrchestrationTestCoordinator(t)
	allowed, err := coord.resolveAgentTools(nil, []string{"view", "write"})
	require.NoError(t, err)
	require.Equal(t, []string{"view", "write"}, allowed)

	_, err = coord.resolveAgentTools(nil, []string{"bash"})
	require.ErrorContains(t, err, "subagent-safe")
}

func TestNativeAgentRecursionLimit(t *testing.T) {
	coord, parentID, providerID := newOrchestrationTestCoordinator(t)
	agent := newMockAgent(providerID, 256, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		t.Fatal("agent must not run beyond recursion limit")
		return nil, nil
	})
	params, err := json.Marshal(AgentParams{Prompt: "too deep"})
	require.NoError(t, err)
	ctx := orchestrationToolContext(t.Context(), parentID)
	ctx = context.WithValue(ctx, orchestrationDepthKey{}, maxAgentDepth)
	response, err := coord.newAgentTool(agent).Run(ctx, fantasy.ToolCall{ID: "call", Name: AgentToolName, Input: string(params)})
	require.NoError(t, err)
	require.True(t, response.IsError)
	require.Contains(t, response.Content, "recursion depth")
}

func TestAgentToolSchemaExposesNativeOrchestration(t *testing.T) {
	coord, _, providerID := newOrchestrationTestCoordinator(t)
	agent := newMockAgent(providerID, 256, func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
		return nil, nil
	})
	data, err := json.Marshal(coord.newAgentTool(agent).Info().Parameters)
	require.NoError(t, err)
	schema := string(data)
	for _, field := range []string{"tasks", "concurrency", "background", "run_id", "token_budget", "synthesis_prompt"} {
		require.Contains(t, schema, field)
	}
}

func TestAgentOrchestratorDrainsWorkersThatIgnoreCancellation(t *testing.T) {
	t.Parallel()
	manager := newAgentOrchestrator(t.TempDir())
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	job, err := manager.Start(t.Context(), AgentRunSnapshot{
		RunID: "agent-drain", State: AgentRunQueued, ParentSessionID: "parent", StartedAt: time.Now().UTC(),
	}, normalizedAgentPlan{Mode: "parallel", Concurrency: 1}, func(ctx context.Context, run *agentRun) {
		run.update(manager, func(snapshot *AgentRunSnapshot) { snapshot.State = AgentRunRunning })
		result := make(chan AgentTaskResult, 1)
		go func() {
			// Simulate a worker that ignores ctx and finishes on its own.
			once.Do(func() { close(started) })
			<-release
			result <- finishAgentTask(AgentTaskResult{ID: "stubborn"}, AgentTaskSucceeded, "done", "", 0)
		}()
		type completion struct {
			index  int
			result AgentTaskResult
		}
		completed := make(chan completion, 1)
		go func() {
			completed <- completion{index: 0, result: <-result}
		}()
		select {
		case <-completed:
		case <-ctx.Done():
		}
		// Drain the worker even though it ignored cancellation.
		<-completed
	})
	require.NoError(t, err)
	<-started
	job.cancel()
	select {
	case <-job.done:
		t.Fatal("run finished while the stubborn worker was still blocked")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-job.done:
	case <-time.After(time.Second):
		t.Fatal("run did not finish after the stubborn worker completed")
	}
}

func TestAgentRunPersistenceClosesStaleTasksInTerminalRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manager := newAgentOrchestrator(dir)
	require.NoError(t, manager.persist(AgentRunSnapshot{
		RunID: "agent-stale", State: AgentRunFailed, ParentSessionID: "parent", StartedAt: time.Now().UTC(),
		FinishedAt: ptrTime(time.Now().UTC()),
		Tasks: []AgentTaskResult{
			{ID: "done", State: AgentTaskSucceeded},
			{ID: "stuck", State: AgentTaskRunning},
		},
	}))

	reloaded := newAgentOrchestrator(dir)
	snapshot, err := reloaded.Snapshot("agent-stale", "parent")
	require.NoError(t, err)
	require.Equal(t, AgentRunFailed, snapshot.State)
	require.Equal(t, AgentTaskSucceeded, snapshot.Tasks[0].State)
	require.Equal(t, AgentTaskCanceled, snapshot.Tasks[1].State)
	require.NotEmpty(t, snapshot.Tasks[1].Error)
	require.NotNil(t, snapshot.Tasks[1].FinishedAt)
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func newOrchestrationTestCoordinator(t *testing.T) (*coordinator, string, string) {
	t.Helper()
	const providerID = "orchestration-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	coord.cfg.Config().Options.DataDirectory = t.TempDir()
	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, coord.Close()) })
	return coord, parent.ID, providerID
}

func invokeAgentTool(t *testing.T, tool fantasy.AgentTool, parentID string, params AgentParams) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	response, err := tool.Run(orchestrationToolContext(t.Context(), parentID), fantasy.ToolCall{
		ID: "call-" + strings.ReplaceAll(t.Name(), "/", "-"), Name: AgentToolName, Input: string(input),
	})
	require.NoError(t, err)
	return response
}

func orchestrationToolContext(ctx context.Context, parentID string) context.Context {
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, parentID)
	return context.WithValue(ctx, tools.MessageIDContextKey, "message-orchestration")
}
