# Persistent state reference

This document inventories everything Ultra writes to disk, how child-session
output is sanitized before persistence, the retention/pruning/quarantine
behavior, and the honest encryption-at-rest story. It supports the
[security policy](../SECURITY.md) and the [threat model](security.md). The
documentation uses the default data directory names; override `--data-dir` /
`option data-dir` to relocate.

## Persistent state inventory

| State | Default location | Content / sensitivity |
| ----- | ---------------- | --------------------- |
| **SQLite database** | Data directory (`ultra.db`; per-project default `.ultra/`) | Sessions, message history, providers/model state, read-files ledger (`read_files`), todos, persistence. **Contains tool inputs/outputs — including anything a session echoed.** High sensitivity. |
| **Agent run snapshots** | Data directory `agents/runs/<run_id>.json` | Versioned, supervised orchestration snapshots (tasks, outputs, errors, usage) — secret-redacted at write time. Medium-high; see redaction limits. |
| **Logs** | `logs/` under the data directory | Debug/operational logs. May contain paths and tool metadata; not necessarily redacted. Low-medium. |
| **Global / project config** | `$XDG_CONFIG_HOME/ultra/` (`~/.config/ultra`), project `ultrarc` / `ultra.json`, credentials | Trusted config; may hold provider tokens, MCP server creds, hook definitions. **Secrets live here in plaintext.** Highest sensitivity. |
| **Skill definitions** | `~/.config/.../skills`, `.ultra/skills` | Trusted, prompt-executable content. Medium. |
| **Prompt history** | `history/` under data dir (if enabled) | Past prompts send to the model. Medium-high. |

The actual directory resolution: the data dir defaults to `.ultra` (legacy
`.crush`), discovered upward within the project boundary, else derived from
the working directory or `XDG_DATA_HOME` / `~/.local/share`. Everything Ultra
writes wire-encode all session activity, so the data dir accumulates a complete
transcript of what the agent saw and did.

## Child-session sanitation

"Child sessions" — sub-agent runs under the native orchestration runtime and Fabric-backed delegates — are not stored verbatim:

- **Per-worker transcripts are ephemeral.** Structured delegation worker
  transcripts are discarded after their bounded result + usage are folded into
  the durable snapshot.
- **Snapshots are bounded and redacted.** `PruneAgentRuns` and the snapshot
  encoder truncate outputs, errors, and persistence errors through
  `redactAgentSecrets` (regex `key = value` for `api[_-]?key`, `access[_-]?token`,
  `password`, `secret`, etc., then `64 KiB` catch-bound).
- **Quarantine receipts**: malformed / unparseable JSON records are removed and
  replaced by a bounded **metadata-only quarantine receipt**, so no unredacted
  garbage persists silently.
- **In-flight recovery**: work found after an exclusively-locked restart is
  retained as `interrupted`, not silently lost (an integrity property, not a
  leak).
- Recursion is bounded (depth 3, 128 tasks, 32 M token output lifetime cap)
  before anything is persisted.

So the durable snapshot is a sanitized, bounded projection — but the **primary
session transcript in the SQLite DB is not** redacted by this pipeline. See
threat-model redaction limitations.

## Retention and pruning

- **Run snapshots age out**: terminal records expire after **30 days** (age
  default). Age-based and count-based pruning are exposed via
  `ultra runs prune` (`--dry-run` to preview, `--data-dir` to target). A zero
  `maxAge` disables age pruning; `-1` disables count pruning.
- **Prune conservatively**: `PruneAgentRuns` only removes *old valid terminal*
  snapshots; active, malformed, mismatched, unsupported, and unrelated JSON
  records are never removed. A maintenance lock serializes the pass.
- **Sessions / messages**: the SQLite DB is the durable store; session-level
  cleanup is user-controlled via the session manager / `ultra session` commands
  and DB migrations, not auto-expired. Nothing in the primary transcript is
  auto-redacted at write time.
- **Read-files history**: `read_files` tracks per-session file reads
  (path, `read_at`) for context/re-fetching; it grows with usage and is
  cascade-deleted with its session.
- **Logs**: `logs/` grow until rotated: retained until the rotation policy
  the user configures. They're not secret-scrubbed on a pathological basis.

## Quarantine

A quarantine here has one concrete meaning: when the orchestrator reads an
existing snapshot at load and the JSON fails to parse or the record is invalid
(match/ID/schema), the file is not exposed as live state. It is held back /
replaced by a bounded metadata-only receipt that omits the unparseable bytes;
the raw garbage does not flow into status/list responses and is not re-run.
This is integrity cleaning, distinct from the 30-day age pruning.

## Encryption at rest — the caveat

**Ultra does not encrypt its data directory or SQLite database.** Everything
above is stored **in plaintext** on disk, subject to the owning user's
filesystem permissions (POSIX ownership/`mode`, Windows ACLs) and any
filesystem-level encryption (LUKS, FileVault, BitLocker, eCryptfs) you enable
independently.

Consequences:

- Anyone who can read the data dir can reconstruct the agent's entire
  transcript, credentials in config, tokens, and provider state.
- Logs, history, and snapshots may contain secrets that failed regex
  redaction; the DB transcript is not redacted at all.
- Ultra does not manage key material, provide a passphrase unlock, or encrypt
  client/workspace traffic beyond whatever the (local) socket transport
  supports. Persistent protection is the operating environment's
  responsibility.
- For sensitive projects: ensure the data dir and config home are on
  encrypted storage with restrictive permissions, do not run `--yolo` /
  `--debug` against untrusted repos, and treat the dir with the same care as
  your SSH keys.

## Minimal hygiene checklist

- Keep `.ultra` / data-dir, `~/.config/ultra`, and any global `.ultrarc` off
  shared or web-served mounts, and check file permissions.
- Prefer `read-only` / `accept-edits` over `yolo` for non-interactive runs
  unless you truly trust the working set.
- Use `ultra runs prune --dry-run` first; keep 30-day aging as a floor which
  alerts you if snapshot growth surprises you.
- When a session touches secrets, it lives in the DB unredacted; delete the
  session (session manager) if it shouldn't survive.

## See also

- [Security policy](../SECURITY.md)
- [Threat model](security.md)
- [Hooks documentation](hooks/README.md)