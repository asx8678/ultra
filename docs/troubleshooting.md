# Troubleshooting

Common failure modes for a running Ultra — provider and model connection
problems, LSP and MCP issues, data-directory contention, durability, and
provider quotas. Start with the [diagnostics](operations.md#diagnostics--logs)
section of [operations](operations.md) to locate logs and directories before
drilling into a specific symptom.

## 1. Provider / model failures

**Symptom:** model selection or message send fails with an auth, quota, or
connection error.

**Check**

1. Confirm the provider and model are configured — `provider list` / the
   model picker (Ctrl+L) and [configuration.md](configuration.md).
2. For key-based providers, verify the right environment variable is set
   (e.g. `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`). Config
   resolution performs shell-style expansion over environment, and `ULTRA_*`
   is re-prefixed (`ULTRA_ANTHROPIC_API_KEY` selects the same credentials).
3. `ULTRA_DISABLE_DEFAULT_PROVIDERS=true` disables the embedded catalog; if a
   custom-only setup is expected, that is intentional. `ULTRA_DISABLE_PROVIDER_AUTO_UPDATE=true`
   stops outbound catalog refresh — some providers will then refuse to
   resolve new models.
4. Check the tail of `<data_dir>/logs/ultra.log` for the raw provider error
   (see [operations diagnostics](operations.md#diagnostics--logs)).

**Notes**
- A provider that is not in the embedded catalog (a local OpenAI-compatible or
  Anthropic-compatible endpoint, for example) must be configured explicitly;
  otherwise the large model silently falls back to the small tier.
- `token_budget`, `max_output_tokens`, and `timeout_seconds` constraints may
  surface as orchestration errors — see
  [agent_runs.md — limits](agent_runs.md#limits-defaults-and-caps).

## 2. LSP failures

**Symptom**
Language-server-backed context unavailable; warnings about a server task's
language server failing to start; slow indexing.

**Checklist**

- Confirm the LSP is discoverable. Ultra auto-discovers servers and starts
  them on demand; a missing binary for the configured server yields a "failed
  to start language server" style error. Install or point the server
  executable on `PATH`.
- LSP servers are configured via `lsp` in [config](config/README.md). A server
  pinned to a path that does not exist will not start.
- Symptoms in the log are usually at info/debug level — `ultra logs`
  sets debug by default (see [operations](operations.md#diagnostics--logs)).
- Some servers require project-serialized settings or a working directory;
  if it starts but the model "sees nothing," verify the server version works
  from your terminal for the same file.

## 3. MCP failures

**Symptom**
An MCP-backed tool errors, times out, or reports "tool not found" even though
the server is configured.

**Checklist**

- Verify the MCP server is actually added: `mcp add` in
  [config](config/README.md). `stdio`, `http`, and `sse` transports are
  supported.
- Effective tool list is computed per agent. In particular, orchestration
  workers with an explicit `tools: []` deny **all** tools including MCP
  (see [agent_runs.md — limits](agent_runs.md#limits-defaults-and-caps)). If a
  worker cannot see an MCP tool, add it to the worker's allowlist.
- Check the log for a transport-level error (connection refused, handshake
  timeout, exit of a `stdio` subprocess).
- Some MCP servers expose an extra tool only after an initial call that
  initializes/authenticates; allow the session to complete that.

## 4. Data-directory lock

**Symptom**
Starting Ultra in a directory reports the data directory is "already in use by
another Ultra process".

**Details**
The exclusive, non-blocking `ultra.lock` inside the data directory is held by
one process at a time (`ultra.lock` is never unlinked by design — flock is
keyed by inode). A lingering or crashed earlier process, or a second live
process pointed at the same data dir, will contend.

**Resolution**

- Inspect `<data_dir>/ultra.lock` — it records the owner's `pid`, `version`,
  and `started_at`. If that PID is stale (the process is gone), the lock file
  is safe — a new process re-takes it automatically because the OS file
  descriptor is what matters.
- Ensure you are not running two Ultra instances against the same directory
  (e.g. a leftover `ultra server` plus a foreground `ultra`).
- `ULTRA_SKIP_DATADIR_LOCK=<truthy>` disables the lock as an escape hatch for
  filesystems without advisory locking — do **not** use it in normal
  operation, because it removes mutual exclusion.

A different, related lock lives in the run store (`<data_dir>/agents/runs/.lock`).
It serializes orchestration state; a run-store *maintenance* (`ultra runs
prune`) will refuse the store if a normal process is already holding it (see
[agent_runs.md](agent_runs.md) and [operations.md](operations.md)).

## 5. Persistence / run-store failures

**Symptom**
"run durability degraded", `durability_status: degraded`, `persistence_error`
on a snapshot, or startup warnings about quarantined run files.

**Checklist**

- Degraded durability means a persistence write failed after the snapshot was
  sealed; the in-memory result is still returned. Check disk space and the
  log path `<data_dir>/agents/runs/<run_id>.json`.
- Startup quarantines (symlink, oversized, unreadable, corrupt, id-mismatch,
  unsupported-version, invalid-state, invalid-size) move the file under
  `<data_dir>/agents/runs/quarantine/` with a receipt explaining the reason.
  A schema-version conflict (run written by a future build) will be rejected —
  see [version-log.md](version-log.md).
- The snapshot byte cap is 10 MB; if a run carries very large output it is
  truncated iteratively but a persistence failure is still possible on quotas.
- Partial `run_dir` writes are atomic; a torn file is treated as corrupt and
  quarantined rather than half-recovered.

## 6. Quota / resource failures

**Symptom**
Orchestration returns errors like "agent tree exceeds its lifetime limit",
"maximum agent recursion depth 3 reached", "concurrency must be between 1 and
16", or "at most 32 agent tasks are allowed".

**Mitigation** — all are enforced limits, not defects:

| Error | Cap | Raise by... |
|---|---|---|
| `at most 32 agent tasks` | 32 per run | split into smaller runs |
| `concurrency must be 1..16` | 16 | use defaults |
| `maximum agent recursion depth 3` | 3 | flatten the tree |
| `tree exceeds its lifetime limit (128 tasks / 32M output tokens)` | 128 tasks / 32M tokens per tree | multiple trees, reduce output |
| `token_budget … exceeds` | budget ≤ 32M | reduce budget / raise per-task caps consistently |

Quota enforcement reserves the full per-worker output allowance for
unbounded tasks up front, so a tree of many unspecified tasks can hit the
tree ceiling even when actual usage is low; set `max_output_tokens` to keep
reservations honest. A worker exceeding its `max_output_tokens` is failed with
an explicit message.

## Diagnostics order

1. `ultra dirs` — confirm data/config paths.
2. `ultra logs` — read `<data_dir>/logs/ultra.log`.
3. Inspect `<data_dir>/ultra.lock` if a lock error appeared.
4. Inspect `<data_dir>/agents/runs/quarantine/` receipts if run-store
   warnings appeared.
5. Cross-check the limits in [agent_runs.md](agent_runs.md) for orchestration
   errors.