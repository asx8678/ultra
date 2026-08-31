# Contributing

Thanks for your interest in contributing to Ultra! Ultra is a terminal-based
AI coding assistant built in Go using the Charm ecosystem.

## Getting Started

1. Fork the repository and clone your fork.
2. Ensure Go 1.27+ is installed (`go 1.27.0` in `go.mod`).
3. Read `AGENTS.md` at the repository root — it contains the architecture
   overview and the project's development guide.

## Development Commands

- **Build:** `go build .`
- **Test:** `task test` or `go test ./...`
- **Update golden files:** `go test ./... -update`
- **Lint:** `task lint:fix`
- **Format:** `task fmt` (gofumpt)
- **Modernize:** `task modernize`

## Code Style

- Use `goimports` grouping (stdlib, external, internal).
- Format with `gofumpt`, enforced in CI.
- Follow standard Go naming conventions.
- Return errors explicitly, wrap with `fmt.Errorf`.
- Prefer `context.Context` as the first parameter.
- Log messages must start with a capital letter
  (enforced by `task lint:log`).
- Comments on their own lines start with a capital letter and end with a
  period.
- Tests: use `require`, prefer `t.Parallel()`, `t.SetEnv()`, and
  `t.Tempdir()`.

## Commit Messages

Use semantic commits:

```
feat: add new capability
fix: correct a bug
chore: housekeeping
docs: documentation changes
refactor: behavior-preserving restructure
```

Keep the subject to one line.

## Testing with Mock Providers

When writing provider-related tests, use the mock providers to avoid API
calls (see `AGENTS.md` for the pattern via `config.UseMockProviders`).

## Pull Requests

- Tie changes to an issue where applicable.
- Include tests and update documentation (`docs/`) when behavior changes.
- Ensure CI (build, lint, security) passes.

## Governance and Releases

See [docs/version-log.md](docs/version-log.md) and the **Governance and
Releases** section of [README.md](README.md) for release hygiene (version
tags, checksums, SBOM, provenance). Note that Ultra is currently **Alpha**.

Please also review [docs/index.md](docs/index.md) for the full documentation
map.