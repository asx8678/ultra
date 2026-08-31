# Security Policy

This document applies to **Ultra**, the terminal-based AI coding assistant
(module `github.com/asx8678/ultra`). It describes supported versions,
how to report vulnerabilities, the disclosure response SLA, and the areas of
the codebase that are the most security-sensitive.

For the accompanying technical material see:

- [Threat model](docs/security.md) — trust boundaries, the tool/permission
  model, prompt injection, and transport caveats.
- [Persistent state reference](docs/security-reference.md) — what Ultra
  writes to disk, what is retained, pruned, quarantined, and the
  encryption-at-rest reality.

## Supported Versions

Ultra is built from tagged releases produced by its release pipeline. The
**latest stable release** is the only currently supported release line.
Pre-release, nightly, staged, and `devel` builds (e.g. `ultra` binaries built
from the working tree, or builds without `-ldflags` version injection) are
**not** covered by the security fix commitment.

| Version status      | Supported |
| ------------------- | --------- |
| Latest stable tag   | ✅ Yes      |
| Previous stable     | ⚠️ Critical- and high-severity fixes only, on a best-effort basis |
| Staged / pre-release| ⛔ No      |
| `devel` / untagged  | ⛔ No      |

If you run from source, track the latest tag for fixes.

## Reporting a vulnerability

**Do not file a public issue** for a vulnerability. Secrets and exploit
details must never appear in a public tracker or public commit.

Privately report vulnerabilities to:

```
security@ultra.example
```

Include, when possible:

- The affected version (ideally the tag, or the commit hash).
- A minimal, reproducible description of the issue.
- The impact you believe it has (privilege escalation, secret exposure,
  persistence tampering, prompt injection is *not* a vuln on its own —
  see [docs/security.md](docs/security.md#prompt-injection), etc.).
- Any crash logs, unsanitized stack traces, or test programs you can share.

For unhandled-secret or data-loss scenarios, mark the subject with `[URGENT]`.

## Response SLA

After a report is received and acknowledged:

- **Acknowledgment**: within **2 business days** the triager confirms receipt
  and assigns a case reference.
- **Initial assessment**: within **5 business days** we reply with a triage
  (confirmed / not reproducible / intended‑behavior) and a rough severity.
- **Fix cadence**:
  - **Critical** — a patch (or a clearly documented workaround) is released
    **as soon as possible** and no later than **14 calendar days**.
  - **High / Medium** — targeted in the next stable release, generally within
    **30 calendar days**.
  - **Low** — scheduled; no strict deadline.
- **Disclosure**: we prefer coordinated public disclosure once a fix is
  available, and will credit you unless you ask to stay anonymous.

Severity follows the standard Common Vulnerability Scoring System (CVSS) as a
closest effort.

## Security-sensitive areas

These components are the highest-risk surfaces. Changes to them undergo extra
review, and failure here often translates directly to host compromise or secret
leakage:

1. **Tools and permissions** (`internal/agent/tools/`,
   `internal/permission/`). Shell execution (`bash`), file edits/writes, the
   `fabric_exec` envelope, and the `ask` / `read-only` / `accept-edits` /
   `yolo` permission modes and per-tool allowlists. Any new tool must be
   added to the permission model deliberately.
2. **Hooks** (`internal/hooks/`, `internal/agent/hooked_tool.go`). User shell
   hooks run before permission checks and can `allow`, `deny`, `halt`, rewrite
   tool input, or inject context. A hook is trusted code; validating or
   transforming untrusted bytes through a hook is a privilege boundary.
3. **Agent orchestration** (`internal/agent/`). The native multi-agent runtime,
   recursive delegation, worker allowlists, output budgets, and run snapshot
   assembly and secret redaction.
4. **Persistence** (`internal/db/`, `internal/session/`). The SQLite database
   and its migrations/content are the durable record of everything a session
   did, including tool inputs and outputs.
5. **MCP and LSP** (`internal/agent/tools/mcp/`, `internal/lsp/`). Third-party
   MCP servers and language servers are spawned or connected to; they can
   inject tools, launch subprocesses, serve content, and read/write on the
   host. Treat them as equal to a user's shell.
6. **Fabric sandbox** (`internal/fabric/`, `internal/fabric/gojasandbox/`).
   The pure-Go ECMAScript sandbox that runs guest TypeScript/JS with no
   ambient filesystem, process, or network bindings. Sandbox escape here is a
   direct host-risk.
7. **Config hooks** (`internal/shellconfig/`, `internal/modconfig` /
   `internal/config/`). `ultrarc` is trusted code executed in a full shell;
   `$(...)` in `ultra.json` runs at load time. Loading config from an
   untrusted directory is treated as running untrusted code.

## General expectations

- **Never put secrets in a public issue.** API keys, tokens, `.env` files,
  private project paths, and personal data belong in a private report; redact
  them before filing anything public.
- Ultra follows **fail-closed** security around tool execution: unknown tools
  are denied, and prompt/member content can never silently grant itself
  privileges beyond what the configured permission mode allows.
- Report phishing, supply-chain, and dependency-related issues through the
  same private channel when they affect Ultra's runtime.

## Linked model

[SECURITY.md](SECURITY.md) ◆ [Threat model](docs/security.md) ◆ [Persistent
state](docs/security-reference.md)