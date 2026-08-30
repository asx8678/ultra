Execute a checked TypeScript program against Ultra capabilities through one
pinned Fabric registry view.

Prefer this tool for complex coding work involving multiple calls, repository
investigation, parallel reads/searches, multi-file changes, or staged changes
and verification. Use a direct native tool for one genuinely trivial operation;
switch to Fabric as soon as the task needs discovery or more than one call. Set
`display.title` to a short, concrete activity label so Ultra can show what the
execution is doing while it runs. Every nested call still passes through
Ultra's session permission policy, authoritative JSON Schema validation,
cancellation, output bounds, and audit trace. Guest code has no ambient
filesystem, process, or network access.

Call Ultra tools through the `host` namespace, for example
`await host.view({path: "README.md"})`. Put long literals in `strings` and read
them from the immutable `π` object, for example `π.body`, rather than embedding
them in `code`. Use `Promise.all` for independent calls. Compile diagnostics
execute no actions, and calls outside the committed capability view fail
closed.
