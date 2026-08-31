Launch and supervise native Go subagents. A prompt-only call runs one agent and returns plain text for backward compatibility.

Use `tasks` for structured orchestration. Each task has an `id`, `prompt`, optional `depends_on`, configured `model` tier (`large` or `small`), subagent-safe `tools`, `cwd`, `max_output_tokens`, `timeout_seconds`, and bounded `recursive` delegation.

Modes:
- `parallel`: run independent tasks concurrently; explicit dependencies are honored.
- `sequential`: chain each task after the previous task and pass dependency results forward.
- `graph`: execute an explicit dependency DAG.
- `council`: run members independently, then launch a synthesis agent over all responses.

Set `concurrency` from 1 to 16. Set `token_budget` to enforce a shared aggregate output-token allowance. Use `action: "spawn"` or `background: true` to return a durable `run_id` immediately. Supervise it with `action: "status"`, `"wait"`, `"list"`, or `"cancel"`. Run state and structured task results are persisted under Ultra's data directory.

Use this tool directly for agent orchestration even when Fabric Code Mode is enabled. It is implemented entirely in Go and does not require TypeScript.
