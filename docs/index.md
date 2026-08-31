# Ultra Documentation Hub

Technical documentation for running and maintaining Ultra. The docs under
`docs/` cover operation, deployment, configuration, and the durable agent-orchestration
run store; the two existing directories — [config](config/) and [hooks](hooks/)
— hold the user-facing reference for configuration and hook events.

## New guides (this directory)

| Guide | What it covers |
|---|---|
| [agent_runs.md](agent_runs.md) | Orchestration lifecycle: the `run` / `spawn` / `wait` / `status` / `list` / `cancel` actions, parallel / sequential / graph / council modes, limits (32 tasks, concurrency cap 16, recursion depth 3, tree caps), cancellation and non-cooperative workers, persistence & restart recovery, and the `ultra runs prune` command. |
| [operations.md](operations.md) | Day-two care: backup (data dir, run snapshots, SQLite), restore, prune, graceful shutdown, diagnostics & logs, telemetry, performance notes. |
| [troubleshooting.md](troubleshooting.md) | Provider / LSP / MCP failures, data-directory lock, persistence and quarantined-snapshot failures, quota errors. |
| [deployment.md](deployment.md) | Build matrix, binary layout, data-directory discovery, multi-user and shared-data-dir caveats, local-only prune, signals. |
| [configuration.md](configuration.md) | Options precedence, environment variables, config-file discovery (`crushrc` / `crush.json`), data-directory, context files (AGENTS / CRUSH / CLAUDE / GEMINI). |
| [version-log.md](version-log.md) | Alpha status, semantic-version policy, persisted-run schema, `currentAgentRunSchemaVersion = 1`, config compatibility, migration, upgrade/rollback guidance. |

## Existing documentation directories

- [config](config/) — user-facing configuration reference: the `provider`,
  `model`, `mcp`, `lsp`, `hook`, `permissions`, and `option` commands, plus
  config composition and versioning ([config/README.md](config/README.md),
  [config/FUTURE.md](config/FUTURE.md)).
- [hooks](hooks/) — the user-facing hook protocol: event types, runner
  semantics, and input/output contracts ([hooks/README.md](hooks/README.md),
  [hooks/FUTURE.md](hooks/FUTURE.md), [hooks/examples](hooks/examples)).

## Suggested reading order

1. **New to orchestration?** Start with [agent_runs.md](agent_runs.md) — the
   lifecycle, modes, and limits.
2. **Where does state live?** [configuration.md](configuration.md) → the
   data directory, then [deployment.md](deployment.md).
3. **Running it for real?** [operations.md](operations.md) for backup/restore
   and graceful shutdown, [troubleshooting.md](troubleshooting.md) for failure
   modes, and [version-log.md](version-log.md) before upgrading.
4. **Hands-on configuration?** The [config](config/) directory has the
   concrete commands; the [hooks](hooks/) directory covers scripted hooks.

## Cross-cutting

- [../AGENTS.md](../AGENTS.md) — the development guide for Ultra itself
  (architecture, build/test/lint commands, styling).
- [../README.md](../README.md) — install and feature overview.