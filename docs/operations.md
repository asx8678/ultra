# Operations

Day-to-day care of a running Ultra installation: backing up and restoring
state, pruning durable orchestration history, graceful shutdown, logging and
diagnostics, and performance notes.

Related reading: [agent runs](agent_runs.md) describes the durable-run
lifecycle this page backs up; [deployment](deployment.md) covers where state
lives and how processes are supervised; [configuration](configuration.md)
explains data-directory discovery.

## Where state lives

State is split across a project-local *data directory* and user-level config
locations. Full discovery rules are in
[deployment.md — data-directory discovery](deployment.md) and
[configuration.md — data directory](configuration.md).

The most important on-disk artifacts:

| Artifact | Path (project data dir) |
|---|---|
| Sessions / message DB | `<data_dir>/ultra.db` (SQLite) |
| Orchestration snapshots | `<data_dir>/agents/runs/*.json` |
| Orchestration lock | `<data_dir>/agents/runs/.lock` |
| Diagnostic log | `<data_dir>/logs/ultra.log` |
| Data-dir lock | `<data_dir>/ultra.lock` (owner info, never unlinked) |

The default project data dir is `<project>/.ultra`; legacy `.crush` is opened
in place if present. Run snapshots, the SQLite database, and the log are the
persistent things you need to back up.

## Backup

Because a graceful shutdown is not required to produce a consistent backup,
the safest procedure is to run backups from a quiescent process (or, if the
app is live, accept that in-flight writes may be mid-transaction).

Minimal consistent backup of one project:

```
tar czf ultra-backup.tar.gz .ultra
```

That captures `ultra.db`, `agents/runs/*.json`, and `logs/`. Highlights that
affect restore correctness:

- Run snapshots are written atomically (fsync + rename), so a copy taken at
  any instant is never a torn byte; a restarted process will mark anything
  left `running`/`queued` as `interrupted`. There is no crash-repair needed
  beyond startup recovery.
- The SQLite database should ideally be copied while no process has it open
  for writing. Copying a live `ultra.db` may capture an in-transit
  transaction; a subsequent graceful startup performs the normal recovery.
- You do **not** need to preserve `ultra.lock` or `.lock`. They are advisory
  and re-created as needed.

To back up only durable run history (excluding the DB and logs):

```
mkdir -p backup && cp -a .ultra/agents .ultra/agents backup/
```

Run snapshots on disk are already redacted of common secrets (bearer tokens,
API keys, passwords, private keys, a subset of cloud credentials), so the
run-tree files are relatively safe to archive; still treat them as sensitive.

## Restore

Restore is a copy-in-place:

1. Stop all Ultra processes using the target data directory.
2. Replace `<data_dir>` with your backup (keeping it at the same path, or
   update the `data-directory` option / `--data-dir` flag to point at it).
3. Start Ultra. On load it acquires the run-dir lock, recovers each snapshot,
   migrates schema-version-0 files, and flags anything `running`/`queued` as
   `interrupted`.

If you restore side-by-side into a new directory, set the data directory
accordingly — see [configuration](configuration.md).

## Prune

Durable run history grows over time and is bounded by retention:
[`ultra runs prune`](agent_runs.md#the-runs-command). Defaults prune terminal
runs older than 30 days and keep at most 1000 terminal runs; active runs are
never pruned. Use `--dry-run` before destructive passes. Prune is local-only
(`--host` is rejected); run it on the machine that owns the data.

## Graceful shutdown

Ultra responds to `SIGINT` and `SIGTERM` (on Unix; PowerShell users rely on
Ctrl+C / `Stop-Process`). On receipt it gracefully:

- closes the orchestration layer: cancels run trees and drains
  cooperative workers within the orchestrator close deadline;
- persists pending runs as `interrupted`/`degraded` if the deadline is hit;
- releases the run-directory lock only after detached workers have stopped,
  or at process exit.

A hard `SIGKILL`/`os.Kill` cannot be intercepted — that is expected and the
durable store is designed to be recoverable from it (see
[agent_runs.md persistence](agent_runs.md#persistence--restart-recovery)).
Prefer `SIGINT`/`SIGTERM` for ordinary shutdown.

## Diagnostics & logs

- **Logs**: `ultra logs` tails `<data_dir>/logs/ultra.log`; flags include
  `--tail <n>` (default 1000), `--follow`, `--data-dir`, and `--cwd`. The
  default log level is set to debug by the command for diagnostics.
- **Directories**: `ultra dirs` prints the resolved config and data
  directories (a fast sanity check when state is not where you expect).
- **Lock owner**: if the data dir is contended, `ultra.lock` records the
  owner's `pid`, `version`, and `started_at`; the contention error surfaces
  this. See [troubleshooting.md](troubleshooting.md).
- **Crash recovery**: startup logging warns (with reasons and paths) about
  quarantined or oversized run snapshots and degraded persistence.

## Telemetry

Ultra does not enable external telemetry by default. It ships an embedded
provider catalog and does not phone home unless you explicitly configure a
remote (`CATWALK_URL`). The legacy `metrics`/`notification_style` options are
accepted for compatibility but no longer drive external reporting — see
[configuration.md](configuration.md) and the [config docs](config/README.md).

## Performance notes

- The data-directory lock is advisory; the run-store lock serializes
  writes to `<data_dir>/agents/runs` across processes.
- Snapshot writes are synchronous (fsync + directory sync) at every lifecycle
  transition, which favors durability over throughput; long trees produce
  many small durable writes. `.ultra`'s `agents/runs` directory is where that
  accumulation concentrates.
- Run history is bounded by the loader's startup caps (entry and byte counts),
  the single-snapshot 10 MB limit, terminal retention (30 days / 1000 runs),
  and quarantine receipt caps — so the store does not grow unboundedly.
- Task output is truncated before persistence (a byte cap and, as a final
  guard, iterative reduction to fit the snapshot limit), keeping files small.
- The project builds with `CGO_ENABLED=0` and the `greenteagc` experiment and
  uses automatic PGO; see [deployment.md](deployment.md).

If serving many users, see the [shared-data-dir caveats](deployment.md) in
the deployment notes.