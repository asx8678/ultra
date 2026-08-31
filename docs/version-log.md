# Version Log & Compatibility

Status, versioning policy, and schema-compatibility guarantees for Ultra's
persisted state. Read [configuration.md](configuration.md) for config-file
semantics and [operations.md](operations.md) for backing up that state.

## Status: Alpha

Ultra is **Alpha software** (see the [README](../README.md) warning): APIs,
configuration, and persisted run formats may change between releases, and it
is not yet recommended for unattended production workloads. Treat every
release as potentially moving these contracts, and keep a backup before
upgrading ([operations](operations.md#backup)).

## Semantic-version policy

Releases follow [Semantic Versioning](https://semver.org). The `task release`
workflow computes the next version with `svu` (`svu next --always`) against
the existing tags and signs a new annotated tag, refusing to run from a dirty
tree or a branch other than `main`. `internal/version.Version` (stamped by
`-ldflags`, or from the module version under `go install`) reports the running
build; it is the value recorded in `<data_dir>/ultra.lock`.

Because the product is pre-1.0, a `MINOR` bump may still change config formats
or persisted schemas; check the changelog for migration notes before each
upgrade.

## Persisted-run schema

Durable orchestration snapshots are written as
`<data_dir>/agents/runs/<run_id>.json` with a top-level `schema_version`
field (`AgentRunSnapshot`). Current value (see
`internal/agent/agent_orchestration.go`):

```
currentAgentRunSchemaVersion = 1
```

Guarantees around the version field:

- **Version 0** is the pre-versioning format and is migrated **in place** to
  the current version on load (and its missing finish-times are backfilled).
- **Unknown/unsupported versions** (anything not `0` or
  `currentAgentRunSchemaVersion`, e.g. a file written by a *newer* build) are
  quarantined — not deleted — so a downgrade that cannot read them still
  leaves them inspected under `agents/runs/quarantine/`.
- Writes always stamp `currentAgentRunSchemaVersion` and canonicalize the
  snapshot (validating IDs, mode, task states, token fields, and byte/size
  caps) before fsync+rename.
- `status`/`wait`/`list`/`cancel` operate on the in-memory + on-disk snapshots;
  the run record is the authoritative, durable outcome.

## Config compatibility

Config files are deep-merged across discovery ([configuration](configuration.md)).
The file *names* are version-agnostic, but Ultra reads both conventions with an
explicit priority order, and the modern `ultrarc`/`ultra.json` names take
precedence over the legacy `crushrc`/`crush.json` when they overlap. Legacy
names remain readable at lower priority during migration, so a mixed project
does not break — newer values win deterministically, and conflicting overlaps
produce a warning. The global data config path is also migrated to the
`ultra`/`crush` split. Config serialization ("Config versioning") is documented
in the [config docs](config/README.md).

## Migration notes

- **Current run-schema version is `1`** (`currentAgentRunSchemaVersion = 1`).
  Files stamped `0` migrate to `1` on load; any later value is unsupported and
  quarantined.
- The `crush`/`ultra` config rename is transparent: authoritative paths
  (`~/.config/ultra`, `~/.local/share/ultra`) take precedence, legacy
  `crush*` paths still load at lower priority.

## Upgrade & rollback guidance

**Upgrade**

1. Stop all Ultra processes using the target data directory.
2. Back up the data directory ([operations — backup](operations.md#backup)).
3. Install/point at the new binary (version stamped at build).
4. Start; the loader performs schema migration (0→current), flags
   interrupted runs, and re-persists changed snapshots. Degraded writes are
   surfaced in `durability_status`.

**Rollback**

1. Stop the new build.
2. Restore the pre-upgrade data-dir backup, or start the older binary
   **against the backup**. Because newer snapshots may carry
   `schema_version` values the older build cannot read, do **not** point the
   old binary at a data dir written by the new one: unsupported-version files
   are quarantined, not rolled back. The safe path is: restore the backup,
   then start the older binary.

**Compatibility check**

- Confirm via `ultra dirs` the data dir being touched.
- Confirm the binary version stamps (`ultra` — prints
  `internal/version.Version`; see the [deployment](deployment.md) versioning
  note) matches what wrote that data dir.

See [troubleshooting.md](troubleshooting.md) for recovery of quarantined or
future-version snapshot files.