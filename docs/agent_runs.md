# Agent Orchestration & Runs

Native Go agent orchestration lets a single LLM conversation supervise a tree
of independent *workers*. Every orchestration is durable: its lifecycle is
persisted to disk after every transition, survives restart, and is manageable
through a small set of actions plus a maintenance command.

This page is the operator-orientation to orchestration. It complements the
[configuration](configuration.md) and [version notes](version-log.md). For
lifecycle/backup concerns see [operations](operations.md), and for failure
modes see [troubleshooting](troubleshooting.md).

## Actions

The `agent` tool accepts an `action` field. Prompt-only calls (empty `action`,
a `prompt`, and no other parameters) keep the original single-agent behavior.

| Action   | Behavior |
|----------|----------|
| `run`    | Execute a plan now. Synchronous by default: the call blocks until the run finishes and returns the full snapshot. With `background: true` it behaves like `spawn`. |
| `spawn`  | Return immediately and supervise the run in the background. Set `background: true` to get the same behavior from `run`. |
| `wait`   | Block until the referenced run (`run_id`) reaches a terminal state, then return its snapshot. |
| `status` | Return the current snapshot of a run (`run_id`) without waiting. |
| `list`   | Return metadata snapshots for all runs owned by the calling session, newest first. Output fields are stripped to keep the response bounded (task `output` is removed and marked `output_truncated`). |
| `cancel` | Cancel a run (`run_id`). Cancelling a tree root also cancels every descendant worker. |

Background runs carry a hard lifetime limit of one hour; if they have not
finished in that window the tree context is cancelled and the run is marked
`canceled`/`interrupted`.

When a foreground `wait` races caller cancellation, the implementation
prefers a completed snapshot; if the run instead outlives the call, the run
ID is surfaced so it can be supervised later rather than being orphaned.

## Lifecycle states

Runs: `queued`, `running`, `succeeded`, `failed`, `canceled`, `interrupted`.
Tasks: `pending`, `running`, `succeeded`, `failed`, `skipped`, `canceled`.

A run whose worker(s) never complete during graceful shutdown is persisted as
`interrupted` and recovered as such on the next start. A run with any
`failed`, `skipped`, or `canceled` task is marked `failed`; every task
`pending` or `running` when the run ends is folded into the terminal state
and the snapshot is persisted before the run is sealed.

## Modes

Plans run in one of four modes (lowercased; unknown values are rejected):

- **`parallel`** (default) — all tasks start opportunistically, bounded by
  `concurrency`. This is what a plan with tasks and no `depends_on` / explicit
  mode uses.
- **`sequential`** (aliases `chain`, `pipeline` every accepted then forced
  concurrency to 1) — tasks are chained so task *n* depends on task *n-1*.
- **`graph`** — tasks wire explicitly through `depends_on` into a DAG.
  Dependencies are validated acyclically up front; a cycle is an error. A task
  whose dependency failed is `skipped`.
- **`council`** — the member tasks run (bounded by concurrency), then a
  synthesis judge task is appended automatically. The judge depends on every
  member. Its prompt comes from `synthesis_prompt`, or a default: "Synthesize
  the council responses into one decisive answer…". Council mode allows at most
  31 member tasks (32 minus the judge).

Dependency output is passed to a dependent worker inside a
`BEGIN UNTRUSTED DEPENDENCY DATA` block, capped at 64 KiB, and the worker is
explicitly warned to treat it only as evidence — never as instructions or as
permission to change tools/scope.

## Limits (defaults and caps)

| Limit | Value |
|---|---|
| Max tasks per run | 32 (`maxAgentTasks`) |
| Concurrency (default) | 4 (`defaultAgentConcurrency`) |
| Concurrency (hard max) | 16 (`maxAgentConcurrency`) |
| Recursion (max agent depth) | 3 (`maxAgentDepth`) |
| Tree-wide task cap (whole run tree) | 128 (`maxAgentTreeTasks`) |
| Tree-wide output-token cap | 32,000,000 (`32 × 1,000,000`) |
| Per-worker output tokens | up to 1,000,000 (`maxAgentOutputTokens`) |
| Worker timeout | per-task `timeout_seconds`, max 86,400 |
| Background run budget | 1 hour |
| Token `token_budget` range | 0 … 32,000,000 |
| Dependency payload | ≤ 64 KiB (`maxDependencyBytes`) |

Per-worker controls (all optional on a task):

- `model` — `large` (default) or `small`, mapped to the configured tier.
- `cwd` — working directory, absolute or relative; it is validated against the
  workspace and cannot escape it.
- `tools` — explicit allowlist of subagent-safe native tools; empty list denies
  *all* tools including MCP. A non-empty list that contains anything not
  subagent-safe is rejected.
- `max_output_tokens` — per-worker output ceiling (default: the shared
  allowance).
- `timeout_seconds` — per-worker timeout (≤ 86_400).
- `recursive` — allow bounded recursive delegation (a nested `agent` tool).

`token_budget` is divided across workers that do not set their own cap,
reserving at least one token per open task and rejecting budgets that exceed
the tree ceiling.

## Recursion

A worker with `recursive: true` gets the `agent` tool back, so it can spawn its
own tree. Depth is tracked on the context and capped at `maxAgentDepth` (3);
exceeding it returns "maximum agent recursion depth 3 reached". Recursion is
always bounded by the same tree-wide task and token caps above — a tree cannot
over-allocate across generations.

## Cancellation & non-cooperative workers

`cancel` (or the owning context being cancelled) stops scheduling, marks
`pending` tasks `canceled`, and requests cancellation of running workers. Go
cannot forcibly terminate an arbitrary goroutine, so workers that ignore
cancellation are handled with a bounded grace period (the drain window) rather
than a hard kill:

- Running workers are drained for a short grace window (`worker_drain_wait`,
  default a few seconds).
- Any worker still running when the window expires is marked `canceled` with
  error "worker did not stop before the cancellation grace period".
- Such workers are tallied as *detached*; they are no longer owners of the
  run's lifecycle. On shutdown, if detached workers are still > 0 the
  orchestrator keeps its directory lease until process exit so another
  process cannot recover in-process state.

The graceful-close drain is bounded by the orchestrator close deadline
(`orchestrator_close_wait`); if it expires, remaining runs are persisted as
`interrupted` with a "shutdown deadline exceeded" error before the run is
sealed.

## Persistence & restart recovery

Every lifecycle transition is written synchronously to
`<data_dir>/agents/runs/<run_id>.json` via an atomic write (temp file +
`fsync` + rename + directory sync), so no visible snapshot is ever a torn
byte. Task output is capped in bytes, truncated as needed, and secrets are
redacted before anything hits disk. Durability is reported per snapshot as
`durable`, or `degraded` with a redacted `persistence_error` when writing the
disk failed.

On load (startup or reload) the orchestrator:

1. Acquires a directory lock (`<data_dir>/agents/runs/.lock`) so only one
   process mutates run state.
2. Reads every `*.json` snapshot, applying structural limits (bounded entry
   and byte counts, symlink/oversize/corrupt/oversize rejection).
3. Migrates schema version 0 files in place to the current version.
4. Marks any `queued`/`running` runs that survived a crash as `interrupted`
   with "Ultra exited before the supervised run completed", and folds their
   tasks into `canceled`.
5. Applies retention (age 30 days, keep 1000) to terminal history only.
   Active runs are always retained so they can be recovered.
6. Re-persists any run whose canonical form changed, and surfaces
   `degraded` durability if that write fails.

## The `runs` command

The durable run store is managed by a small command under `ultra`.

### `ultra runs prune`

Removes old *terminal* run snapshots from local storage. It never touches
active runs, and it never removes malformed, mismatched, unsupported-version,
or unrelated JSON records.

```
ultra runs prune [--older-than DURATION] [--keep N] [--dry-run]
```

| Flag | Default | meaning |
|---|---|---|
| `--older-than` | `720h` (30 days) | remove only terminal runs finished longer ago than this |
| `--keep` | `1000` | retain at most this many newest terminal runs; `-1` disables count pruning |
| `--dry-run` | `false` | report what would be removed without deleting files |

Output is a single line `scanned=N removed=N bytes=N dry_run=true`.

Retention rules that `prune` shares with the startup loader:

- Retention applies **only** to terminal runs. A zero/very small
  `--older-than` never removes an active run.
- A zero `--older-than` disables age pruning; `--keep -1` disables count
  pruning.
- Before deleting, each candidate is reopened and revalidated so a replaced
  pathname cannot cause removal of an unrelated file.

`prune` is intentionally *local-only*: `--host` is rejected for it — run it on
the host that owns the data. It deliberately avoids full config initialization
so a maintenance dry-run never executes shell config, migrates files, or
contacts a provider. See [deployment](deployment.md) and
[operations](operations.md).

### Related actions

`list`, `status`, `wait`, and `cancel` in the preceding table all operate on
run snapshots via the `agent` tool. Listing/status are read-only; `cancel` is
the only mutating action there.