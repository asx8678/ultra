# Configuration

How Ultra decides its behavior: precedence order, environment variables,
config-file discovery (`crushrc` / `crush.json`), the data directory, and the
project context files it injects into prompts.

The user-facing config vocabulary lives in the [config docs](config/README.md)
(providers, models, MCP, LSP, hooks, permissions, options); this page is the
specification of *discovery and precedence*. For versioning of persisted
state, see [version-log.md](version-log.md). For orchestration-specific
settings see [agent_runs.md](agent_runs.md).

## Options precedence

Effective settings are the merge of several sources. Highest to lowest:

1. **Explicit defaults/command flags** — flags such as `--cwd`, `--data-dir`
   and per-subcommand flags (e.g. `--dry-run`). `--data-dir` overrides any
   data-directory discovery.
2. **Project config (deepest wins)** — config files are discovered by walking
   up from the working directory and merging; deeper directories override
   shallower ones.
3. **User/global config** — always loaded, at lower priority than project.
4. **Embedded defaults** — built-in model/provider catalog and option
   defaults.

Within a single directory, if more than one file defines a key, priority is
listed high→low:

```
.ultrarc        (hidden shell config)
ultrarc         (shell config)
.ultra.json     (hidden JSON)
ultra.json      (JSON)
.crushrc        (legacy shell)
crushrc         (legacy shell)
.crush.json     (legacy JSON)
crush.json      (legacy JSON)
```

So the modern `ultrarc` beats modern `ultra.json`, and `crushrc`/`crush.json`
remain readable at lower priority during migration. When a directory contains
*both* a modern (`.ultra*`) and a legacy (`crush*`) file that overlap, the
modern takes precedence and the load warns about conflicting keys.

Shell config (`.ultrarc`, `ultrarc`, `crushrc`) may be executed as Bash
(respect it; see the [config docs](config/README.md)), so it is never loaded
from the writable machine-*data* directory — only from config-sized locations.

## Configuration files & discovery

Config files can be named either to the modern `ultra` convention or the
legacy `crush` convention (both exist; `crushrc` and `crush`/`crush.json`
are the migration names). Ultra searches, in order, at each directory from the
working directory upward to the project root (git work-tree root when
detectable, else the working directory itself):

1. Global user config (always): `~/.config/ultra/ultrarc` (or
   `ULTRA_GLOBAL_CONFIG`/XDG variants).
2. System config path, legacy global `crush` locations.
3. Per-directory discovered configs (`ultrarc`/`ultra.json`/`crushrc`/
   `crush.json` and dot-variants), deepest first.

All sources are deep-merged with later (higher-priority) values winning.

## Environment variables

Env vars are read at config load. `ULTRA_*` overrides map onto their
unprefixed twin: `ULTRA_ANTHROPIC_API_KEY` resolves to the same provider
credential slot as `ANTHROPIC_API_KEY`, and the `ULTRA_*` forms work for
both the stack providers (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
`GEMINI_API_KEY`, etc.) and Ultra's own tunables.

| Variable | Meaning |
|---|---|
| `ULTRA_GLOBAL_CONFIG` | override the global **config** directory |
| `ULTRA_GLOBAL_DATA` | override the global *data* config directory |
| `ULTRA_CACHE_DIR` | override the cache directory |
| `ULTRA_SKILLS_DIR` | override the global Agent-Skills directory |
| `ULTRA_DISABLE_DEFAULT_PROVIDERS` | skip the embedded provider catalog |
| `ULTRA_DISABLE_PROVIDER_AUTO_UPDATE` | disable outbound provider-catalog refresh |
| `ULTRA_SKIP_DATADIR_LOCK` | truthy: bypass the data-dir advisory lock (not for normal use) |
| `ULTRA_SERVER_IDLE_TIMEOUT` | server idle-shutdown seconds (0 restores default linger) |
| `ULTRA_SERVER_DETACH_GRACE` | server detach grace seconds |
| `ULTRA_SERVER_READY_TIMEOUT` | server ready timeout (Go duration) |
| `ULTRA_CLIENT_SERVER` | truthy: connect as a client to a server process |
| `ULTRA_UI_DEBUG` | `"true"` enables TUI debug traces |
| `ULTRA_DISABLE_ANTHROPIC_CACHE` | truthy: disable the Anthropic prompt cache |
| `ULTRA_CORE_UTILS` | truthy/false: force the shell's core-utils flavor |
| `ULTRA_VERSION` | injected into shell-config environment |
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `VERCEL_API_KEY`, `GEMINI_API_KEY`, `ZAI_API_KEY`, `MINIMAX_API_KEY`, `SYNTHETIC_API_KEY`, `HF_TOKEN`, `HYPER_API_KEY`, `AWS_*`, … | provider credentials (parsed from env by the embedded catalog) |

Values in config may use shell-style expansion (`$VAR`, `${VAR:-default}`,
`$(command)`, quoting) via the embedded shell resolver. Unset variables expand
to empty by default; use `${VAR:?message}` to require a value loudly.

## Data directory

The data directory holds **writable machine state**: `ultra.db` (sessions/
messages), `agents/runs/*.json` (durable run snapshots), and `logs/`. It is
distinct from user *config*.

Resolution, in order: explicit `--data-dir`/option → a discovered project
`.ultra` (walking up, bounded to the project root) → a legacy `.crush` →
defaults to `<working_dir>/.ultra`. Relative paths resolve against the working
directory. See [deployment.md — data-directory discovery](deployment.md).

Pointing several processes at one data directory is supported only
sequentially (advisory lock); see the [shared-data-dir caveats](deployment.md).

## Context files

On startup Ultra gathers **project context** from well-known files in the
working tree and injects their content into the system prompt. Files are
globbing (order defines read precedence) and `.local` variants are merged
(project + per-machine/local):

Modern/legacy names read by default (deduplicated):

```
.github/copilot-instructions.md
.cursorrules
.cursor/rules/
CLAUDE.md            CLAUDE.local.md
GEMINI.md            gemini.md
ultra.md             ultra.local.md
Ultra.md             Ultra.local.md
ULTRA.md             ULTRA.local.md
AGENTS.md            agents.md          Agents.md
```

`.local` variants layer the same file (e.g. `AGENTS.md` + `AGENTS.local.md`).
The UI's project-initialization creates the file named by `initialize_as`
(default `AGENTS.md`).

**Global context** comes from `ULTRA.md` and `~/.config/AGENTS.md` (via
`global_context_paths`), alongside any working-dir `context_paths` you
configure. `CRUSH.md`-style legacy context is not part of the modern
default set; the project-level [AGENTS.md](../AGENTS.md) names the context
files Ultra reads.

## See also

- [config docs](config/README.md) — the `provider` / `model` / `lsp` / `mcp` /
  `hook` / `permissions` / `option` commands build the files this page
  describes.
- [hooks docs](hooks/README.md) — user-defined shell hooks fired per event.
- [config FUTURE](config/FUTURE.md) and [hooks FUTURE](hooks/FUTURE.md) —
  planned changes that affect config format reconciliation.