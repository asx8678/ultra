# Deployment

Notes on building, laying out, and supervising Ultra in production-like
environments. This is the pairing piece for [operations](operations.md) (day
two) and [troubleshooting](troubleshooting.md) (failure modes).

## Build matrix

Ultra is a single Go module built to a native binary. The project defaults to
`CGO_ENABLED=0` and `GOEXPERIMENT=greenteagc`, and uses automatic profile-
guided optimization (a checked-in `default.pgo`).

Supported targets (see README): macOS, Linux, Windows (PowerShell), WSL,
Android, FreeBSD, OpenBSD, and NetBSD.

Common build entry points (from `Taskfile.yaml`):

| Command | Produces | Purpose |
|---|---|---|
| `task build` | `ultra` (`ultra.exe`) | Standard binary; `-ldflags` bake in `internal/version.Version` from `git describe` when available. |
| `task build:without-fabric` | `ultra-without-fabric(.exe)` | Build **without** the Fabric sandbox bridge (`-tags fabric_disabled`); the specialized sandbox-free binary. |
| `go build .` / `go install github.com/asx8678/ultra@latest` | binary | Plain `go install` in a release embeds the go.mod `version` (see `internal/version`). |
| `task install` | install | `go install` plus fetching tags and stamping version. |
| `task release` | tag | Tags the next semantic version (via `svu`) on `main`; requires clean tree and passing build/snapshot CI. |
| `task test:without-fabric` | n/a | Tests the `fabric_disabled` build. |

Rebuild-sensitive inputs include all `.go` files plus the embedded system
prompts (`internal/agent/**/*.md` and `*.md.tpl`) and the Hyper catalog
`provider.json`.

## Binary layout

Ultra ships as a **single binary**. System prompt templates
(`coder.md.tpl`, `task.md.tpl`, `initialize.md.tpl`) and the embedded provider
catalog are compiled in via `go:embed` — there are no required runtime data
resources to place alongside the executable.

Optional binaries/components:

- `ultra-without-fabric` as built above, if you deploy the no-Fabric variant.
- An external browser/[Fabric] sandbox bridge, only when the Fabric execution
  mode is enabled and used.

Versioning: `internal/version.Version` is stamped at build time by `-ldflags`
(`-X github.com/asx8678/ultra/internal/version.Version=<v>`); `go install`
sets it from the module version. `internal/version.Commit` and `BuildID` are
similarly stamped/derived. This value is written into `<data_dir>/ultra.lock`
owner metadata and compared between a daemon and its clients.

## Data-directory discovery

The data directory (project state: SQLite `ultra.db`, `agents/runs/*`, logs)
is resolved, in order:

1. Explicit `--data-dir` flag / `data_directory` option (an absolute path, or
   a relative path resolved against the working directory).
2. An existing project `<project>/.ultra` directory found by walking up from
   the working directory, bounded by the project root (git work-tree root when
   detectable; otherwise the working directory itself). This boundary stops
   state files above the project from being adopted.
3. A legacy existing `.crush` directory (opened in place so prior sessions
   remain visible), again bounded to the project.
4. Otherwise `defaults` to `<working_dir>/.ultra`.

A related distinction: **user config** and **data** live in
`~/.config/ultra` and `~/.local/share/ultra` (XDG), overridable with
`ULTRA_GLOBAL_CONFIG` and `ULTRA_GLOBAL_DATA`. Project state stays in the
project data dir; user-level overrides stay in the global data dir.

For the full list (including all `ULTRA_*` environment overrides), see
[configuration.md](configuration.md).

## Multi-user and shared-data-dir caveats

- **Data dir is exclusive.** A data directory is used by one process at a
  time (`ultra.lock`). Do **not** point several supervisor processes or a
  cluster at one data dir and expect concurrent read/write — you will
  serialize on the lock and get contention errors.
- **The run-store is single-writer.** `<data_dir>/agents/runs/.lock`
  serializes orchestration mutation across processes; recovery and
  maintenance mutually exclude each other.
- **The `--host`/remote model is explicit.** Some commands are local-only by
  design. `ultra runs prune` explicitly rejects `--host` — run maintenance on
  the machine that owns the data dir rather than from a remote frontend.
- **Shared remote filesystem caveats:** advisory `flock` semantics must be
  honored by the filesystem (NFS/SMB without advisory locking will not provide
  mutual exclusion). `ULTRA_SKIP_DATADIR_LOCK` exists only as an escape hatch
  for filesystems without advisory locking and is not recommended for normal
  use.
- **Per-user workspaces vs shared binary.** The binary is shared; the data
  directory is per-project/per-user. This is the intended topology: shared
  executable, unshared state.

## Local-only prune

`ultra runs prune` deletes only local terminal run snapshots and must target
the machine that owns the data (see [agent_runs.md](agent_runs.md#the-runs-command)).
It deliberately avoids `config.Init` so a dry-run never executes shell config,
migrates files, or contacts a provider — safe to drive in a cron job with
`--dry-run` first.

## Signals

- `SIGINT` / `SIGTERM` trigger graceful shutdown: orchestration runs are
  cancelled/drained and pending snapshots persisted as `interrupted` before
  exit (see [operations.md — graceful shutdown](operations.md#graceful-shutdown)).
- `SIGKILL` / `os.Kill` cannot be intercepted; the durable run-store is still
  recoverable afterward (startup recovery marks `running`/`queued` runs
  `interrupted`).
- Supervision: the embedded server path builds a `signal.Notify` set of
  `SIGINT` (+ `SIGTERM` on Unix hosts, not Windows). When wrapping in a
  supervisor, send `SIGTERM` to get clean teardown rather than killing the
  process tree.

[Fabric]: https://github.com/danielmiessler/fabric