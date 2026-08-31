package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"

	agentprompt "github.com/asx8678/ultra/internal/agent/prompt"
	agenttools "github.com/asx8678/ultra/internal/agent/tools"
	"github.com/asx8678/ultra/internal/config"
	"github.com/asx8678/ultra/internal/fsext"
	"github.com/asx8678/ultra/internal/toolmeta"
)

const (
	defaultAgentConcurrency = 4
	maxAgentConcurrency     = 16
	maxAgentTasks           = 32
	maxAgentDepth           = 3
	maxAgentOutputTokens    = 1_000_000
	maxDependencyBytes      = 64 * 1024
)

type orchestrationDepthKey struct{}

// AgentTask describes one independently supervised worker in an orchestration.
type AgentTask struct {
	ID              string   `json:"id,omitempty" description:"Stable task identifier; generated when omitted"`
	Prompt          string   `json:"prompt" description:"Task for this worker"`
	DependsOn       []string `json:"depends_on,omitempty" description:"Task IDs that must succeed first"`
	Model           string   `json:"model,omitempty" description:"Configured model tier: large or small"`
	Tools           []string `json:"tools,omitempty" description:"Per-agent allowlist of subagent-safe native Go tools"`
	CWD             string   `json:"cwd,omitempty" description:"Working directory, absolute or relative to the workspace"`
	MaxOutputTokens int64    `json:"max_output_tokens,omitempty" description:"Maximum output tokens for this worker"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty" description:"Worker timeout in seconds"`
	Recursive       bool     `json:"recursive,omitempty" description:"Allow bounded recursive agent delegation"`
}

// AgentParams is the native Go agent orchestration API. Prompt-only calls keep
// the original single-agent behavior.
type AgentParams struct {
	Action          string      `json:"action,omitempty" description:"run, spawn, wait, status, list, or cancel"`
	Prompt          string      `json:"prompt,omitempty" description:"Backward-compatible single-agent task"`
	Tasks           []AgentTask `json:"tasks,omitempty" description:"Workers in this orchestration"`
	Mode            string      `json:"mode,omitempty" description:"parallel, sequential, graph, or council"`
	Concurrency     int         `json:"concurrency,omitempty" description:"Maximum workers running at once (1-16)"`
	Background      bool        `json:"background,omitempty" description:"Return immediately and supervise in the background"`
	RunID           string      `json:"run_id,omitempty" description:"Run identifier for wait, status, or cancel"`
	TokenBudget     int64       `json:"token_budget,omitempty" description:"Output-token allowance shared across workers; divided into per-worker caps"`
	SynthesisPrompt string      `json:"synthesis_prompt,omitempty" description:"Council judge instructions"`
}

// AgentTaskState is the durable lifecycle state of one worker.
type AgentTaskState string

// AgentRunState is the durable lifecycle state of an orchestration.
type AgentRunState string

const (
	AgentTaskPending   AgentTaskState = "pending"
	AgentTaskRunning   AgentTaskState = "running"
	AgentTaskSucceeded AgentTaskState = "succeeded"
	AgentTaskFailed    AgentTaskState = "failed"
	AgentTaskSkipped   AgentTaskState = "skipped"
	AgentTaskCanceled  AgentTaskState = "canceled"

	AgentRunQueued      AgentRunState = "queued"
	AgentRunRunning     AgentRunState = "running"
	AgentRunSucceeded   AgentRunState = "succeeded"
	AgentRunFailed      AgentRunState = "failed"
	AgentRunCanceled    AgentRunState = "canceled"
	AgentRunInterrupted AgentRunState = "interrupted"
)

// AgentTaskResult is the structured, durable outcome of one worker.
type AgentTaskResult struct {
	ID              string         `json:"id"`
	State           AgentTaskState `json:"state"`
	Output          string         `json:"output,omitempty"`
	Error           string         `json:"error,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	Model           string         `json:"model,omitempty"`
	CWD             string         `json:"cwd,omitempty"`
	TokensUsed      int64          `json:"tokens_used,omitempty"`
	StartedAt       *time.Time     `json:"started_at,omitempty"`
	FinishedAt      *time.Time     `json:"finished_at,omitempty"`
	MaxOutputTokens int64          `json:"max_output_tokens,omitempty"`
}

// AgentRunSnapshot is persisted after every lifecycle transition.
type AgentRunSnapshot struct {
	RunID           string            `json:"run_id"`
	State           AgentRunState     `json:"state"`
	Mode            string            `json:"mode"`
	Background      bool              `json:"background"`
	ParentSessionID string            `json:"parent_session_id"`
	Concurrency     int               `json:"concurrency"`
	TokenBudget     int64             `json:"token_budget,omitempty"`
	TokensUsed      int64             `json:"tokens_used,omitempty"`
	Tasks           []AgentTaskResult `json:"tasks"`
	Error           string            `json:"error,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
}

type normalizedAgentPlan struct {
	Legacy      bool
	Mode        string
	Concurrency int
	TokenBudget int64
	Tasks       []AgentTask
}

type agentRun struct {
	mu       sync.RWMutex
	manager  *agentOrchestrator
	snapshot AgentRunSnapshot
	plan     normalizedAgentPlan
	done     chan struct{}
	cancel   context.CancelFunc
}

type agentOrchestrator struct {
	mu        sync.RWMutex
	persistMu sync.Mutex
	runs      map[string]*agentRun
	dir       string
	closed    bool
}

func (c *coordinator) handleAgentTool(
	ctx context.Context,
	taskAgent SessionAgent,
	params AgentParams,
	call fantasy.ToolCall,
) (fantasy.ToolResponse, error) {
	action := strings.ToLower(strings.TrimSpace(params.Action))
	if action == "" {
		action = "run"
	}

	sessionID := agenttools.GetSessionFromContext(ctx)
	if sessionID == "" {
		return fantasy.ToolResponse{}, errors.New("session id missing from context")
	}
	manager := c.agentOrchestrator()

	switch action {
	case "status":
		snapshot, err := manager.Snapshot(params.RunID, sessionID)
		return agentSnapshotResponse(snapshot, err)
	case "list":
		return agentSnapshotsResponse(manager.List(sessionID)), nil
	case "wait":
		snapshot, err := manager.Wait(ctx, params.RunID, sessionID)
		return agentSnapshotResponse(snapshot, err)
	case "cancel":
		snapshot, err := manager.Cancel(params.RunID, sessionID)
		return agentSnapshotResponse(snapshot, err)
	case "run", "spawn":
		if depth, _ := ctx.Value(orchestrationDepthKey{}).(int); depth >= maxAgentDepth {
			return fantasy.NewTextErrorResponse(fmt.Sprintf("maximum agent recursion depth %d reached", maxAgentDepth)), nil
		}
	default:
		return fantasy.NewTextErrorResponse("action must be run, spawn, wait, status, list, or cancel"), nil
	}

	messageID := agenttools.GetMessageFromContext(ctx)
	if messageID == "" {
		return fantasy.ToolResponse{}, errors.New("agent message id missing from context")
	}
	plan, err := normalizeAgentPlan(params)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	legacy := isLegacyAgentCall(params)
	plan.Legacy = legacy
	background := params.Background || action == "spawn"
	// Detach the run from the tool call context. A canceled client disconnect
	// must not tear down the persisted run mid-flight; the caller learns the
	// run_id below and can supervise or cancel it explicitly.
	jobCtx := context.WithoutCancel(ctx)
	job, err := manager.Start(jobCtx, AgentRunSnapshot{
		RunID:           newAgentRunID(),
		State:           AgentRunQueued,
		Mode:            plan.Mode,
		Background:      background,
		ParentSessionID: sessionID,
		Concurrency:     plan.Concurrency,
		TokenBudget:     plan.TokenBudget,
		StartedAt:       time.Now().UTC(),
	}, plan, func(runCtx context.Context, run *agentRun) {
		c.executeAgentPlan(runCtx, taskAgent, run, messageID, call.ID)
	})
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if background {
		return agentSnapshotResponse(job.Snapshot(), nil)
	}

	runID := job.Snapshot().RunID
	snapshot, err := manager.Wait(ctx, runID, sessionID)
	if err != nil {
		// The run outlives this call; surface the run_id so the caller can
		// supervise or cancel it instead of leaving it orphaned.
		message := fmt.Sprintf("%s (agent run %q is still supervised; use action \"status\", \"wait\", or \"cancel\" with run_id %q)", err.Error(), runID, runID)
		return agentSnapshotResponse(snapshot, errors.New(message))
	}
	if legacy && len(snapshot.Tasks) == 1 {
		result := snapshot.Tasks[0]
		if result.State != AgentTaskSucceeded {
			return fantasy.NewTextErrorResponse(firstNonEmpty(result.Error, "sub-agent failed")), nil
		}
		return fantasy.NewTextResponse(result.Output), nil
	}
	return agentSnapshotResponse(snapshot, nil)
}

func (c *coordinator) agentOrchestrator() *agentOrchestrator {
	c.orchestratorMu.Lock()
	defer c.orchestratorMu.Unlock()
	if c.orchestrator != nil {
		return c.orchestrator
	}
	dataDir := c.cfg.Config().Options.DataDirectory
	if dataDir == "" {
		dataDir = filepath.Join(c.cfg.WorkingDir(), ".ultra")
	}
	c.orchestrator = newAgentOrchestrator(filepath.Join(dataDir, "agents", "runs"))
	return c.orchestrator
}

func newAgentOrchestrator(dir string) *agentOrchestrator {
	manager := &agentOrchestrator{runs: make(map[string]*agentRun), dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Failed to load agent runs", "directory", dir, "error", err)
		return manager
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var snapshot AgentRunSnapshot
		if json.Unmarshal(data, &snapshot) != nil || !validAgentRunID(snapshot.RunID) {
			continue
		}
		if snapshot.State == AgentRunQueued || snapshot.State == AgentRunRunning {
			now := time.Now().UTC()
			snapshot.State = AgentRunInterrupted
			snapshot.Error = "Ultra exited before the supervised run completed"
			snapshot.FinishedAt = &now
			for index := range snapshot.Tasks {
				if snapshot.Tasks[index].State != AgentTaskPending && snapshot.Tasks[index].State != AgentTaskRunning {
					continue
				}
				snapshot.Tasks[index].State = AgentTaskCanceled
				snapshot.Tasks[index].Error = snapshot.Error
				snapshot.Tasks[index].FinishedAt = &now
			}
		}
		// A crash between the final task update and the run update can leave
		// unfinished tasks inside a terminal run; close them out consistently.
		if isTerminalAgentRunState(snapshot.State) {
			now := time.Now().UTC()
			for index := range snapshot.Tasks {
				if snapshot.Tasks[index].State != AgentTaskPending && snapshot.Tasks[index].State != AgentTaskRunning {
					continue
				}
				snapshot.Tasks[index].State = AgentTaskCanceled
				snapshot.Tasks[index].Error = "agent run finished before this task completed"
				snapshot.Tasks[index].FinishedAt = &now
			}
		}
		run := &agentRun{manager: manager, snapshot: snapshot, done: make(chan struct{})}
		close(run.done)
		manager.runs[snapshot.RunID] = run
		if err := manager.persist(snapshot); err != nil {
			slog.Warn("Failed to persist interrupted agent run", "run_id", snapshot.RunID, "error", err)
		}
	}
	return manager
}

func (m *agentOrchestrator) Start(
	ctx context.Context,
	snapshot AgentRunSnapshot,
	plan normalizedAgentPlan,
	execute func(context.Context, *agentRun),
) (*agentRun, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("agent orchestrator is closed")
	}
	if _, exists := m.runs[snapshot.RunID]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("agent run %q already exists", snapshot.RunID)
	}
	for _, task := range plan.Tasks {
		snapshot.Tasks = append(snapshot.Tasks, AgentTaskResult{
			ID: task.ID, State: AgentTaskPending, Model: firstNonEmpty(task.Model, "large"),
			CWD: task.CWD, MaxOutputTokens: task.MaxOutputTokens,
		})
	}
	runCtx, cancel := context.WithCancel(ctx)
	run := &agentRun{manager: m, snapshot: snapshot, plan: plan, done: make(chan struct{}), cancel: cancel}
	if err := m.persist(snapshot); err != nil {
		cancel()
		m.mu.Unlock()
		return nil, fmt.Errorf("persist initial agent run: %w", err)
	}
	m.runs[snapshot.RunID] = run
	m.mu.Unlock()

	go func() {
		defer close(run.done)
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				now := time.Now().UTC()
				run.update(m, func(snapshot *AgentRunSnapshot) {
					snapshot.State = AgentRunFailed
					snapshot.Error = fmt.Sprintf("agent orchestration panicked: %v", recovered)
					snapshot.FinishedAt = &now
				})
			}
		}()
		execute(runCtx, run)
	}()
	return run, nil
}

func (m *agentOrchestrator) Snapshot(runID, sessionID string) (AgentRunSnapshot, error) {
	if strings.TrimSpace(runID) == "" {
		return AgentRunSnapshot{}, errors.New("run_id is required")
	}
	m.mu.RLock()
	run := m.runs[runID]
	m.mu.RUnlock()
	if run == nil {
		return AgentRunSnapshot{}, fmt.Errorf("agent run %q not found", runID)
	}
	snapshot := run.Snapshot()
	if snapshot.ParentSessionID != sessionID {
		return AgentRunSnapshot{}, fmt.Errorf("agent run %q not found", runID)
	}
	return snapshot, nil
}

func (m *agentOrchestrator) Wait(ctx context.Context, runID, sessionID string) (AgentRunSnapshot, error) {
	_, err := m.Snapshot(runID, sessionID)
	if err != nil {
		return AgentRunSnapshot{}, err
	}
	m.mu.RLock()
	run := m.runs[runID]
	m.mu.RUnlock()
	select {
	case <-ctx.Done():
		return run.Snapshot(), ctx.Err()
	case <-run.done:
		return run.Snapshot(), nil
	default:
	}
	select {
	case <-ctx.Done():
		return run.Snapshot(), ctx.Err()
	case <-run.done:
		return run.Snapshot(), nil
	}
}

func (m *agentOrchestrator) Cancel(runID, sessionID string) (AgentRunSnapshot, error) {
	if _, err := m.Snapshot(runID, sessionID); err != nil {
		return AgentRunSnapshot{}, err
	}
	m.mu.RLock()
	run := m.runs[runID]
	m.mu.RUnlock()
	run.mu.RLock()
	cancel := run.cancel
	run.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	return run.Snapshot(), nil
}

func (m *agentOrchestrator) List(sessionID string) []AgentRunSnapshot {
	m.mu.RLock()
	runs := make([]*agentRun, 0, len(m.runs))
	for _, run := range m.runs {
		runs = append(runs, run)
	}
	m.mu.RUnlock()
	result := make([]AgentRunSnapshot, 0, len(runs))
	for _, run := range runs {
		snapshot := run.Snapshot()
		if snapshot.ParentSessionID == sessionID {
			result = append(result, snapshot)
		}
	}
	slices.SortFunc(result, func(a, b AgentRunSnapshot) int {
		return b.StartedAt.Compare(a.StartedAt)
	})
	return result
}

func (m *agentOrchestrator) Close() {
	m.mu.Lock()
	m.closed = true
	runs := make([]*agentRun, 0, len(m.runs))
	for _, run := range m.runs {
		runs = append(runs, run)
	}
	m.mu.Unlock()
	for _, run := range runs {
		run.mu.RLock()
		cancel := run.cancel
		run.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
	}
	// Coordinator teardown closes provider, graph, Fabric, and database
	// services immediately after this method. Drain every supervised worker so
	// none can outlive and access those dependencies.
	for _, run := range runs {
		<-run.done
	}
}

func (m *agentOrchestrator) persist(snapshot AgentRunSnapshot) error {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	if m.dir == "" {
		return nil
	}
	if !validAgentRunID(snapshot.RunID) {
		return errors.New("agent run ID is invalid")
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return fmt.Errorf("create agent run directory: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent run: %w", err)
	}
	tmp, err := os.CreateTemp(m.dir, snapshot.RunID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create agent run temp file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(m.dir, snapshot.RunID+".json"))
	}
	if err == nil {
		err = fsext.SyncDirectory(m.dir)
	}
	if err != nil {
		return fmt.Errorf("persist agent run %q: %w", snapshot.RunID, err)
	}
	return nil
}

func validAgentRunID(runID string) bool {
	if runID == "" || len(runID) > 128 {
		return false
	}
	for _, character := range runID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (r *agentRun) Snapshot() AgentRunSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneAgentSnapshot(r.snapshot)
}

func (r *agentRun) update(manager *agentOrchestrator, mutate func(*AgentRunSnapshot)) {
	r.mu.Lock()
	mutate(&r.snapshot)
	snapshot := cloneAgentSnapshot(r.snapshot)
	if err := manager.persist(snapshot); err != nil {
		slog.Warn("Failed to persist agent run", "run_id", snapshot.RunID, "error", err)
	}
	r.mu.Unlock()
}

func cloneAgentSnapshot(snapshot AgentRunSnapshot) AgentRunSnapshot {
	clone := snapshot
	clone.Tasks = slices.Clone(snapshot.Tasks)
	return clone
}

func (c *coordinator) executeAgentPlan(
	ctx context.Context,
	fallbackAgent SessionAgent,
	run *agentRun,
	messageID string,
	outerToolCallID string,
) {
	manager := run.manager
	run.update(manager, func(snapshot *AgentRunSnapshot) { snapshot.State = AgentRunRunning })
	plan := run.plan
	states := make([]AgentTaskState, len(plan.Tasks))
	for i := range states {
		states[i] = AgentTaskPending
	}
	results := make(map[string]AgentTaskResult, len(plan.Tasks))
	type completion struct {
		index  int
		result AgentTaskResult
	}
	completed := make(chan completion, len(plan.Tasks))
	running := 0
	finished := 0

	updateTask := func(index int, result AgentTaskResult) {
		run.update(manager, func(snapshot *AgentRunSnapshot) {
			snapshot.Tasks[index] = result
			var tokens int64
			for _, task := range snapshot.Tasks {
				tokens += task.TokensUsed
			}
			snapshot.TokensUsed = tokens
		})
	}

	for finished < len(plan.Tasks) {
		if ctx.Err() != nil {
			for i, state := range states {
				if state != AgentTaskPending {
					continue
				}
				now := time.Now().UTC()
				states[i] = AgentTaskCanceled
				result := AgentTaskResult{ID: plan.Tasks[i].ID, State: AgentTaskCanceled, Error: ctx.Err().Error(), FinishedAt: &now}
				results[result.ID] = result
				updateTask(i, result)
				finished++
			}
		}

		launched := false
		for i, task := range plan.Tasks {
			if running >= plan.Concurrency || states[i] != AgentTaskPending || ctx.Err() != nil {
				continue
			}
			ready, blocked := dependenciesReady(task.DependsOn, results)
			if blocked {
				now := time.Now().UTC()
				states[i] = AgentTaskSkipped
				result := AgentTaskResult{ID: task.ID, State: AgentTaskSkipped, Error: "dependency failed", FinishedAt: &now}
				results[task.ID] = result
				updateTask(i, result)
				finished++
				launched = true
				continue
			}
			if !ready {
				continue
			}
			states[i] = AgentTaskRunning
			running++
			launched = true
			started := time.Now().UTC()
			runningResult := AgentTaskResult{
				ID: task.ID, State: AgentTaskRunning, Model: firstNonEmpty(task.Model, "large"),
				CWD: task.CWD, StartedAt: &started, MaxOutputTokens: task.MaxOutputTokens,
			}
			updateTask(i, runningResult)
			dependencyPrompt := promptWithDependencies(task.Prompt, task.DependsOn, results)
			go func(index int, task AgentTask, taskPrompt string, startedAt time.Time) {
				result := c.executeAgentTask(ctx, fallbackAgent, task, taskPrompt, messageID, outerToolCallID, startedAt, plan.Legacy)
				completed <- completion{index: index, result: result}
			}(i, task, dependencyPrompt, started)
		}

		if running == 0 {
			if finished == len(plan.Tasks) {
				break
			}
			if !launched {
				break
			}
			continue
		}
		select {
		case completion := <-completed:
			running--
			finished++
			states[completion.index] = completion.result.State
			results[completion.result.ID] = completion.result
			updateTask(completion.index, completion.result)
		case <-ctx.Done():
			// The loop head marks pending tasks canceled; workers that keep
			// running despite cancellation are reaped below.
		}
	}

	// Drain workers that did not honor cancellation so their results are
	// recorded and no goroutine outlives the run.
	for running > 0 {
		completion := <-completed
		running--
		states[completion.index] = completion.result.State
		results[completion.result.ID] = completion.result
		updateTask(completion.index, completion.result)
	}

	now := time.Now().UTC()
	run.update(manager, func(snapshot *AgentRunSnapshot) {
		snapshot.FinishedAt = &now
		switch {
		case ctx.Err() != nil:
			snapshot.State = AgentRunCanceled
			snapshot.Error = ctx.Err().Error()
		case slices.Contains(states, AgentTaskFailed) || slices.Contains(states, AgentTaskSkipped) ||
			slices.Contains(states, AgentTaskCanceled):
			snapshot.State = AgentRunFailed
			snapshot.Error = "one or more agent tasks failed"
		default:
			snapshot.State = AgentRunSucceeded
		}
	})
}

func (c *coordinator) executeAgentTask(
	ctx context.Context,
	fallbackAgent SessionAgent,
	task AgentTask,
	taskPrompt string,
	messageID string,
	outerToolCallID string,
	startedAt time.Time,
	legacy bool,
) AgentTaskResult {
	result := AgentTaskResult{
		ID: task.ID, State: AgentTaskRunning, Model: firstNonEmpty(task.Model, "large"),
		CWD: task.CWD, StartedAt: &startedAt, MaxOutputTokens: task.MaxOutputTokens,
	}
	worker := fallbackAgent
	if task.Model != "" || task.CWD != "" || len(task.Tools) > 0 || task.Recursive {
		var err error
		worker, result.CWD, err = c.buildNativeTaskAgent(ctx, task)
		if err != nil {
			return finishAgentTask(result, AgentTaskFailed, "", err.Error(), 0)
		}
	} else if result.CWD == "" {
		result.CWD = c.cfg.WorkingDir()
	}

	taskCtx := context.WithValue(ctx, orchestrationDepthKey{}, agentDepth(ctx)+1)
	if task.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		taskCtx, cancel = context.WithTimeout(taskCtx, time.Duration(task.TimeoutSeconds)*time.Second)
		defer cancel()
	}
	childToolCallID := outerToolCallID + "-" + task.ID
	if task.ID == "task" && legacy {
		childToolCallID = outerToolCallID
	}
	result.SessionID = c.sessions.CreateAgentToolSessionID(messageID, childToolCallID)
	response, usage, err := c.runSubAgentDetailed(taskCtx, subAgentParams{
		Agent: worker, SessionID: agenttools.GetSessionFromContext(ctx), AgentMessageID: messageID,
		ToolCallID: childToolCallID, Prompt: taskPrompt, SessionTitle: "Agent: " + task.ID,
		MaxOutputTokens: task.MaxOutputTokens,
	})
	tokens := usage.TotalTokens
	if tokens == 0 {
		tokens = usage.InputTokens + usage.OutputTokens
	}
	if err != nil {
		return finishAgentTask(result, taskStateForContext(taskCtx), "", err.Error(), tokens)
	}
	if taskCtx.Err() != nil {
		return finishAgentTask(result, AgentTaskCanceled, "", taskCtx.Err().Error(), tokens)
	}
	if response.IsError {
		return finishAgentTask(result, AgentTaskFailed, "", response.Content, tokens)
	}
	return finishAgentTask(result, AgentTaskSucceeded, response.Content, "", tokens)
}

func (c *coordinator) buildNativeTaskAgent(ctx context.Context, task AgentTask) (SessionAgent, string, error) {
	workingDir, err := c.resolveAgentWorkingDir(task.CWD)
	if err != nil {
		return nil, "", err
	}
	agentCfg, ok := c.cfg.Config().Agents[config.AgentTask]
	if !ok {
		return nil, "", errors.New("task agent not configured")
	}
	agentCfg.AllowedTools, err = c.resolveAgentTools(agentCfg.AllowedTools, task.Tools)
	if err != nil {
		return nil, "", err
	}

	large, small, err := c.buildAgentModels(ctx, true)
	if err != nil {
		return nil, "", err
	}
	selected := large
	if task.Model == "small" {
		selected = small
	}
	providerCfg, ok := c.cfg.Config().Providers.Get(selected.ModelCfg.Provider)
	if !ok {
		return nil, "", errModelProviderNotConfigured
	}
	promptTemplate, err := taskPrompt(agentprompt.WithWorkingDir(workingDir))
	if err != nil {
		return nil, "", err
	}
	systemPrompt, err := promptTemplate.Build(ctx, selected.Model.Provider(), selected.Model.Model(), c.cfg)
	if err != nil {
		return nil, "", err
	}
	workerTools, err := c.buildToolsAt(ctx, agentCfg, true, workingDir)
	if err != nil {
		return nil, "", err
	}
	isYolo := c.permissions != nil && c.permissions.SkipRequests()
	worker := NewSessionAgent(SessionAgentOptions{
		LargeModel: selected, SmallModel: selected, SystemPromptPrefix: providerCfg.SystemPromptPrefix,
		SystemPrompt: systemPrompt, IsSubAgent: true, DisableAutoSummarize: c.cfg.Config().Options.DisableAutoSummarize,
		IsYolo: isYolo, Sessions: c.sessions, Messages: c.messages, Tools: workerTools,
		Notify: c.notify, RunComplete: c.runComplete,
	})
	if task.Recursive {
		recursiveTool := c.newAgentTool(worker)
		recursiveTools := wrapToolsWithPolicy([]fantasy.AgentTool{recursiveTool}, c.permissions)
		workerTools = append(workerTools, recursiveTools...)
		worker.SetTools(agenttools.NewCatalog(workerTools).Tools())
	}
	return worker, workingDir, nil
}

func (c *coordinator) resolveAgentWorkingDir(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return c.cfg.WorkingDir(), nil
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.cfg.WorkingDir(), path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve agent cwd: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("agent cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agent cwd %q is not a directory", path)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path), nil
}

func (c *coordinator) resolveAgentTools(defaults, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return slices.Clone(defaults), nil
	}
	coder, ok := c.cfg.Config().Agents[config.AgentCoder]
	if !ok {
		return nil, errCoderAgentNotConfigured
	}
	resolved := make([]string, 0, len(requested))
	for _, name := range requested {
		if name == AgentToolName || name == "fabric_exec" || !slices.Contains(coder.AllowedTools, name) {
			return nil, fmt.Errorf("tool %q is not available to task agents", name)
		}
		descriptor, ok := toolmeta.Lookup(name)
		if !ok || !descriptor.SubagentSafe {
			return nil, fmt.Errorf("tool %q is not marked subagent-safe", name)
		}
		if !slices.Contains(resolved, name) {
			resolved = append(resolved, name)
		}
	}
	return resolved, nil
}

func normalizeAgentPlan(params AgentParams) (normalizedAgentPlan, error) {
	mode := strings.ToLower(strings.TrimSpace(params.Mode))
	if mode == "" {
		mode = "parallel"
	}
	switch mode {
	case "parallel", "graph", "council":
	case "chain", "pipeline", "sequential":
		mode = "sequential"
	default:
		return normalizedAgentPlan{}, errors.New("mode must be parallel, sequential, graph, or council")
	}

	tasks := make([]AgentTask, len(params.Tasks))
	copy(tasks, params.Tasks)
	if len(tasks) == 0 && strings.TrimSpace(params.Prompt) != "" {
		tasks = []AgentTask{{ID: "task", Prompt: params.Prompt}}
	}
	if len(tasks) == 0 {
		return normalizedAgentPlan{}, errors.New("prompt or tasks is required")
	}
	if len(tasks) > maxAgentTasks {
		return normalizedAgentPlan{}, fmt.Errorf("at most %d agent tasks are allowed", maxAgentTasks)
	}

	ids := make(map[string]struct{}, len(tasks)+1)
	for i := range tasks {
		tasks[i].ID = strings.TrimSpace(tasks[i].ID)
		if tasks[i].ID == "" {
			tasks[i].ID = fmt.Sprintf("task-%d", i+1)
		}
		if !validAgentTaskID(tasks[i].ID) {
			return normalizedAgentPlan{}, fmt.Errorf("task id %q must contain only letters, digits, '.', '_', or '-'", tasks[i].ID)
		}
		if _, exists := ids[tasks[i].ID]; exists {
			return normalizedAgentPlan{}, fmt.Errorf("duplicate task id %q", tasks[i].ID)
		}
		ids[tasks[i].ID] = struct{}{}
		tasks[i].Prompt = strings.TrimSpace(tasks[i].Prompt)
		if tasks[i].Prompt == "" {
			return normalizedAgentPlan{}, fmt.Errorf("task %q prompt is required", tasks[i].ID)
		}
		tasks[i].Model = strings.ToLower(strings.TrimSpace(tasks[i].Model))
		if tasks[i].Model != "" && tasks[i].Model != "large" && tasks[i].Model != "small" {
			return normalizedAgentPlan{}, fmt.Errorf("task %q model must be large or small", tasks[i].ID)
		}
		if tasks[i].MaxOutputTokens < 0 || tasks[i].MaxOutputTokens > maxAgentOutputTokens {
			return normalizedAgentPlan{}, fmt.Errorf("task %q max_output_tokens must be between 0 and %d", tasks[i].ID, maxAgentOutputTokens)
		}
		if tasks[i].TimeoutSeconds < 0 || tasks[i].TimeoutSeconds > 24*60*60 {
			return normalizedAgentPlan{}, fmt.Errorf("task %q timeout_seconds must be between 0 and 86400", tasks[i].ID)
		}
	}

	if mode == "sequential" {
		for i := 1; i < len(tasks); i++ {
			if !slices.Contains(tasks[i].DependsOn, tasks[i-1].ID) {
				tasks[i].DependsOn = append(tasks[i].DependsOn, tasks[i-1].ID)
			}
		}
	}
	if mode == "council" {
		if len(tasks) == maxAgentTasks {
			return normalizedAgentPlan{}, fmt.Errorf("council mode allows at most %d member tasks", maxAgentTasks-1)
		}
		judgeID := "synthesis"
		for suffix := 2; ; suffix++ {
			if _, exists := ids[judgeID]; !exists {
				break
			}
			judgeID = fmt.Sprintf("synthesis-%d", suffix)
		}
		dependencies := make([]string, 0, len(tasks))
		for _, task := range tasks {
			dependencies = append(dependencies, task.ID)
		}
		judgePrompt := strings.TrimSpace(params.SynthesisPrompt)
		if judgePrompt == "" {
			judgePrompt = "Synthesize the council responses into one decisive answer. Resolve disagreements and preserve concrete evidence."
		}
		tasks = append(tasks, AgentTask{ID: judgeID, Prompt: judgePrompt, DependsOn: dependencies})
		ids[judgeID] = struct{}{}
	}

	for _, task := range tasks {
		for _, dependency := range task.DependsOn {
			if dependency == task.ID {
				return normalizedAgentPlan{}, fmt.Errorf("task %q cannot depend on itself", task.ID)
			}
			if _, exists := ids[dependency]; !exists {
				return normalizedAgentPlan{}, fmt.Errorf("task %q depends on unknown task %q", task.ID, dependency)
			}
		}
	}
	if err := validateAgentDAG(tasks); err != nil {
		return normalizedAgentPlan{}, err
	}
	if params.TokenBudget < 0 || params.TokenBudget > int64(maxAgentTasks*maxAgentOutputTokens) {
		return normalizedAgentPlan{}, errors.New("token_budget is outside the supported range")
	}
	if err := applyAgentTokenBudget(tasks, params.TokenBudget); err != nil {
		return normalizedAgentPlan{}, err
	}
	concurrency := params.Concurrency
	if concurrency == 0 {
		concurrency = min(defaultAgentConcurrency, len(tasks))
	}
	if concurrency < 1 || concurrency > maxAgentConcurrency {
		return normalizedAgentPlan{}, fmt.Errorf("concurrency must be between 1 and %d", maxAgentConcurrency)
	}
	if mode == "sequential" {
		concurrency = 1
	}
	return normalizedAgentPlan{Mode: mode, Concurrency: concurrency, TokenBudget: params.TokenBudget, Tasks: tasks}, nil
}

func applyAgentTokenBudget(tasks []AgentTask, budget int64) error {
	if budget == 0 {
		return nil
	}
	var explicit int64
	automatic := 0
	for _, task := range tasks {
		if task.MaxOutputTokens > 0 {
			explicit += task.MaxOutputTokens
		} else {
			automatic++
		}
	}
	if explicit > budget {
		return fmt.Errorf("task output limits (%d) exceed token_budget (%d)", explicit, budget)
	}
	remaining := budget - explicit
	if automatic == 0 {
		return nil
	}
	if remaining < int64(automatic) {
		return fmt.Errorf("token_budget must reserve at least one output token for each of %d unbounded tasks", automatic)
	}
	share := remaining / int64(automatic)
	remainder := remaining % int64(automatic)
	for i := range tasks {
		if tasks[i].MaxOutputTokens != 0 {
			continue
		}
		tasks[i].MaxOutputTokens = share
		if remainder > 0 {
			tasks[i].MaxOutputTokens++
			remainder--
		}
	}
	return nil
}

func validateAgentDAG(tasks []AgentTask) error {
	indegree := make(map[string]int, len(tasks))
	dependents := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		indegree[task.ID] = len(task.DependsOn)
		for _, dependency := range task.DependsOn {
			dependents[dependency] = append(dependents[dependency], task.ID)
		}
	}
	queue := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if indegree[task.ID] == 0 {
			queue = append(queue, task.ID)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if visited != len(tasks) {
		return errors.New("agent task dependencies contain a cycle")
	}
	return nil
}

func dependenciesReady(dependencies []string, results map[string]AgentTaskResult) (ready, blocked bool) {
	for _, dependency := range dependencies {
		result, exists := results[dependency]
		if !exists {
			return false, false
		}
		if result.State != AgentTaskSucceeded {
			return false, true
		}
	}
	return true, false
}

func promptWithDependencies(prompt string, dependencies []string, results map[string]AgentTaskResult) string {
	if len(dependencies) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n<dependency_results>\n")
	for _, dependency := range dependencies {
		result := results[dependency]
		fmt.Fprintf(&b, "## %s\n%s\n", dependency, result.Output)
		if b.Len() >= maxDependencyBytes {
			b.WriteString("[dependency output truncated]\n")
			break
		}
	}
	b.WriteString("</dependency_results>")
	value := b.String()
	if len(value) > maxDependencyBytes {
		value = value[:maxDependencyBytes]
	}
	return value
}

func finishAgentTask(result AgentTaskResult, state AgentTaskState, output, errorMessage string, tokens int64) AgentTaskResult {
	now := time.Now().UTC()
	result.State = state
	result.Output = output
	result.Error = errorMessage
	result.TokensUsed = tokens
	result.FinishedAt = &now
	return result
}

func taskStateForContext(ctx context.Context) AgentTaskState {
	if ctx.Err() != nil {
		return AgentTaskCanceled
	}
	return AgentTaskFailed
}

func isTerminalAgentRunState(state AgentRunState) bool {
	switch state {
	case AgentRunSucceeded, AgentRunFailed, AgentRunCanceled, AgentRunInterrupted:
		return true
	default:
		return false
	}
}

func agentDepth(ctx context.Context) int {
	depth, _ := ctx.Value(orchestrationDepthKey{}).(int)
	return depth
}

func validAgentTaskID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func newAgentRunID() string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "agent-" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("agent-%d", time.Now().UnixNano())
}

func isLegacyAgentCall(params AgentParams) bool {
	return params.Action == "" && params.Prompt != "" && len(params.Tasks) == 0 && params.Mode == "" &&
		params.Concurrency == 0 && !params.Background && params.TokenBudget == 0 && params.SynthesisPrompt == ""
}

func agentSnapshotResponse(snapshot AgentRunSnapshot, err error) (fantasy.ToolResponse, error) {
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	data, marshalErr := json.MarshalIndent(snapshot, "", "  ")
	if marshalErr != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("marshal agent run: %w", marshalErr)
	}
	response := fantasy.NewTextResponse(string(data))
	return fantasy.WithResponseMetadata(response, snapshot), nil
}

func agentSnapshotsResponse(snapshots []AgentRunSnapshot) fantasy.ToolResponse {
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error())
	}
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(string(data)), snapshots)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
