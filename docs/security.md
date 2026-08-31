# Ultra threat model

This document describes the boundaries and trust assumptions of Ultra, a
terminal AI coding assistant. It accompanies the [security policy](../SECURITY.md)
and the [persistent-state reference](security-reference.md).

## Principles

Ultra's operating model is:

- **Fail closed**: unknown / unclassified tool calls are denied by default.
- **Sub-agent-safe**: nested, delegated, or Fabric guest work inherits the same
  hooks and permission policy as the top-level agent; a sub-call cannot bypass
  the parent's authorization.
- **Trusted config**: config, hooks, and prompt-content files are treated as
  code, not data (see [Config hooks](../SECURITY.md#security-sensitive-areas)).
- **No ambient privilege**: the only real host capabilities an agent has are
  the ones its tools encode — chiefly a shell and file access — and those are
  gated by permissions.

## Trust boundaries

| Trust level | Entities | Implications |
| ----------- | -------- | ------------ |
| **Trusted host** | The user's OS, installed tools, shell, and any dependencies already on `PATH`. | Ultra is only as safe as the environment it runs in. A compromised host is out of scope. |
| **Trusted user config** | `ultrarc`, `ultra.json`, global/project ULTRA.md, AGENTS.md/CRUSH.md/CLAUDE.md context, skill definitions, hooks. | Executed or injected into the prompt. Treat loading these as running trusted code. |
| **Trusted-but-extensible surfaces** | Installed LSP servers, MCP servers, plugins, third-party tools. | Spawned or connected to with full host capabilities. Equivalent to the user's own shell/editor. |
| **Untrusted by design** | The model's output, repo/member content it reads, MCP/LSP responses, Fabric guest programs, task payloads. | May contain prompt injection or malicious instructions. Denied at the tool/permission layer, not by trusting the model. |

A **primary trust boundary** is: **anything the model reads can influence what
the model does**. Enforcement therefore happens at the *tool* and *permission*
layer, never by trusting the model to behave.

## Fail-closed and sub-agent-safe tool model

- The permission service classifies each tool call with a session `Mode`:
  `ask`, `read-only`, `accept-edits`, or `yolo`, plus per-session allowlist of
  tools (`allowedTools`). Unknown or un-allowlisted tools do **not** run.
- `PreToolUse` hooks compose in config order before permission checks: any
  `deny` blocks, any `halt` ends the turn, `allow` is affirmative pre-approval,
  and silence falls through to the normal permission prompt. A hook's
  `updated_input` is a shallow-merge patch applied to the tool input.
- **Sub-agents do not get a free pass.** `PreToolUse` hooks only intercept the
  *top-level* agent's direct tool calls (so a single delegated turn doesn't
  re-fire hooks N times), but the outer `agent` / `fabric_exec` *tool call
  itself* is hooked and permission-checked. Any capability a sub-agent body
  exercises still passes through the session's hooks and permission policy.
- The Fabric sandbox runs guest programs with **no ambient filesystem,
  process, module, timer, or network bindings**; capability calls are the only
  bridge and route through the same hooks and session permission policy. On
  sandbox builds (`-tags fabric_disabled`), Fabric defaults off and enabling it
  fails closed.
- Recursive delegation is bounded (max depth 3, 128 tasks, output-token
  caps), so a compromised child cannot balloon the delegation tree.

## Prompt injection

**Residual risk**: Ultra does **not** attempt to prevent prompt injection from
content read into context. A malicious `README.md`, an evil-edit diff, an MCP
server response, or a fetched web page can instruct the model to take actions.
That is acceptable by design *only because* the tool/permission layer is the
enforcement point: injected text cannot grant itself permissions.

**Mitigations**:

- Fail-closed permission modes: injected "run X" instructions still hit the
  normal prompt or allowlist.
- Hooks can block, allow-list, or rewrite commands (e.g. a `.gitignore`
  guardrail rehouses `rm -rf /`).
- `read-only` and `accept-edits` modes deny shell and network calls entirely.
- Snapshot redaction removes interpolable secrets from orchestration output
  (see limits below).
- Users are advised to disable/uninstall MCP servers they don't trust, and
  not to launch Ultra in an untrusted directory.

**Residual injection stays injected**: redaction, hooks, and permissions do
**not** sanitize the prompt. If context crafting (`context`/reason strings) is
plausible, treat tool input the model produces as model-described, not alimited
whitelist, and rely on deny/allow decisions.

## YOLO / skip-confirmation mode

`--yolo` (and session mode `yolo`) skips the permission prompt for **all**
tools, including unknown ones. This is the maximum-risk configuration:

- It grants unknown tools too (see `TestSessionModes`: `yolo grants unknown`).
- It disables the interactive authorization gate; only a `deny`/`halt`
  `PreToolUse` hook can still block a call.
- It persists for the session and, when a shared workspace is involved, is
  fixed by the first client (see next section).

`yolo` is intended for trusted, non-interactive/CI automation. Do not run it
against untrusted repositories or with untrusted MCP servers attached.

## Workspace vs data-directory boundary

- **Workspace**: the live state of a given `--cwd`, held in memory and shared
  by every client that opens an SSE stream against the same backend + `--cwd`.
  It is torn down when the last stream disconnects.
- **Data directory** (default `.ultra` in the project, discovered up to the
  project boundary; fallback derived from XDG data dirs). the durable SQLite
  database, `agents/runs/*.json` snapshots, and logs. This is the persistence
  boundary.

The workspace holds **no durable secrets**; anything behavioral or durable goes
through the data directory. A workspace leak is bounded to the lifetime of its
attached streams. The data directory outlives both streams and sessions and
must be protected like credentials (see the encryption caveat in the reference
doc).

## Snapshot redaction limitations

Agent run snapshots (`.ultra/agents/runs/*.json`) are redacted and prevented
from leaking interpolated secrets:

- `redactAgentSecrets` interpolates `api[_-]?key|access[_-]?token|...` `key =
value` patterns, then truncates to 64 KiB.
- Errors, outputs, and persistence errors are all routed through it.

**Limitations**: redaction is **regex-based and bounded**, not a guarantee:
  - A secret not matching the pattern (no obvious key-name near it, or a bare
    value) is not covered.
  - Truncation-bound shape is exactly byte-capped, not content-aware.
  - Multi-line JSON, base64, and secrets interpolated in unrelated fields can
    slip through.
  - Redaction happens at snapshot time; the primary session transcript (in the
    SQLite DB) is **not** redacted this way. Treat session/message content as
    if it may contain the secrets the model logged.

## Shared or multi-client `cwd` caveats

Multiple clients can attach to the same workspace (shared backend, e.g. two
TUIs against `ultra serve`):

- Clients are grouped by **`--cwd`**; same `cwd` = same underlying workspace,
  sharing session list, message history, permission queue, LSP, and MCP state.
- Joining is **implicit** — a second client pointing at the same `--cwd`
  attaches to the existing workspace; nothing verifies "same person".
- The **first client fixes process-wide flags**, notably `--yolo` and `--debug`
  (first-wins). A `--yolo` client can effectively impose yolo on a later
  client sharing the `--cwd`.
- Shared MCP/LSP state means any client in the workspace can observe or steer
  tools spawned for it.

Consequence: running `yolo` on a machine where other processes already share
the `--cwd` weakens *their* authority too, and a privilege control that cannot
identify a unique principal can't enforce per-person boundaries within a
workspace.

## Local socket / pipe transport

Ultra's client/server boundary (a workspace backend) listens on:

- **Unix**: a per-user Unix socket in the runtime dir (`ultra-<uid>.sock`, or
  fallback in `/tmp`).
- **Windows**: a named pipe `\\.\pipe\ultra-<uid>.sock`.
- TCP is possible for remote `--host` use.

**Caveats**:

- The Windows fallback lives under `/tmp`, a world-readable shared dir; the
  socket is not meaningfully access-controlled there beyond the OS socket
  semantics. A local attacker who can connect to it can speak the protocol.
- Unix sockets under the per-user runtime dir are the default and are
  per-UID; the files should be protected by the OS. Ownership/permission
  trivia is the user's responsibility.
- The transport itself is not authenticated end-to-end: it authenticates *the
  local user principal / UID*, not a distinct client identity. Any process
  running as the same UID can connect and drive it.
- Do not expose the remote TCP host to untrusted networks; there is no
  TLS/credential handshake designed for public exposure.

## See also

- [Security policy](../SECURITY.md)
- [Persistent state reference](security-reference.md)
- [Hooks documentation](hooks/README.md)
- [Configuration / tools documentation](config/README.md)