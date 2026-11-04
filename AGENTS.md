# Mu

Mu is a minimal, Go-only native ACP desktop client. Keep the UI in MyGo's `ui` package. Do not introduce a web frontend, TypeScript tooling, or additional product features without a request.

Keep agent startup generic: execute the user-configured command without agent-specific defaults, adapter discovery, or runtime selection. Pi installation and launch configuration belong in test setup and documentation, not client startup logic. Saved agents are named command templates; existing threads retain their command snapshots. Session controls come only from the agent's advertised options, with legacy modes as a fallback. Do not invent model lists, context limits, or cost estimates.

## Greenfield development

Mu has no users depending on its existing data, and its data model is not settled. Do not version the schema or persistence format. Do not write migrations, backward-compatibility conversions, or automatic old-format detection/reset logic.

When making a breaking change to the data model or persistence layer, wipe the existing Mu-owned local data as part of the development task, with Mu stopped and its process lock respected. Do this directly, not through application migration/reset code. This includes disposable local QA state. Never delete agent-owned sessions or workspace files as part of this cleanup. Tests must use isolated data directories.

## Structure

- `main.go`: CLI flags and window startup/shutdown.
- `internal/desktop`: native view; use `ui.NewTester` for interaction tests.
- `internal/app`: synchronized thread state, ACP lifecycle, shared agent connections (`connections.go`), projects and thread folders, permission handling, and persistence orchestration.
- `internal/agent`: stdio ACP connection and agent process.
- `internal/state`: session references and settings with an exclusive process lock and atomic saves. Never persist conversations or SDK session state.
- `internal/agent/testdata/agent`: deterministic stdio agent used by tests.

Read `README.md` and `GLOSSARY.md` for deliberate semantics. Require ACP v1 list/load/close/fork at initialization (not resume, which Mu never calls); reject missing capabilities without fallbacks. Fork uses the SDK's native `session/fork`, which ACP still marks unstable. Remove is local only; agent processes are not sandboxed.

Use MyGo's animation clock and transitions, not sleeps or per-frame timers. Respect reduced motion; never delay streamed text for presentation. Keep the empty overlay layer in `View.Draw`: it supplies a stable parent for overlay enter/exit transitions. Headless interaction tests advance their clock to settle motion; animation tests must also inspect intermediate frames.

The UI is main-thread-only and reads immutable snapshots. Background work changes application state under its mutex and signals a coalesced change channel. Never call blocking process teardown while holding the app mutex. Track detached teardown work in the app wait group so shutdown joins it. Close agent sessions before terminating processes; reconnects must wait for the close barrier, including after removal and re-import under a new local thread ID. Native fork is blocking and must run off the UI thread. A stopping turn must not release its busy state before its cancellation dispatch completes.

One agent connection (process) per agent configuration — saved agent ID plus command snapshot — serves every thread's session; route notifications and permissions by session ID. Discovery lists on that same connection. Do not start and stop processes needlessly: a saved agent's process keeps running while idle until the app closes or that saved configuration is edited or removed; other configurations (one-off commands, older snapshots) terminate with their last user. Every `acquire` must be balanced by a release, usually through `detachLocked`. Do not advertise or implement the ACP v1 client file system or terminal methods; ACP v2 removes them, and agents handle files and commands themselves. Process startup is independent of any one waiting thread. A context deadline cannot interrupt a blocked pipe write: hard deadlines use `Process.Bounded`, which kills the process on timeout; otherwise `Process.Close` lets the agent exit gracefully. Take connections out of use only through `retireLocked`: a configuration's replacement process must not start until the previous one has exited. An error response does not end a session; Stop escalates from `session/cancel` to `session/close` before terminating anything.

New threads get a randomly named folder in the general working directory, or use a project's existing folder. Never delete thread or project folders except Mu's own still-empty folder when thread creation fails. Tests pass a temporary default working directory to `app.Open`; never let them use the real one.

Process session notifications even while idle. Buffer setup notifications and replace the in-memory projection only after successful load/replay. Connection generations reject stale process notifications. Never inject transcript context. Group message chunks by agent message ID when present; do not send client message IDs. Project tool calls structurally and derive their visible text; updates patch only supplied fields. Discovery follows pagination, and never creates sessions or auto-imports them. Only complete listings reconcile metadata; absence is not proof of deletion. Scope session IDs to the agent configuration and command snapshot. Nested SDK session values in snapshots must be deep-copied. Mu uses `github.com/spachava753/acp-sdk`; do not switch to a different ACP SDK. `internal/agent/transport.go` delivers `session/update` notifications itself, in wire order, because SDK v0.6.0 does not order them.

Known tech debt, including that SDK gap and what to change once it is fixed, is tracked in [docs/tech-debt.md](docs/tech-debt.md).

## Checks

For Mu UI changes, visual regressions, or screenshot/animation QA requests, read and follow [mu-visual-qa](.bb/skills/mu-visual-qa/SKILL.md). Capture with the offline harness and inspect the images; passing tests alone is not visual QA.

Run `go test -race ./...`, `go vet ./...`, and `go build -o bin/mu .` after meaningful changes. Tests use an offline fixture with its own session store, separate from Mu’s data directory. The old Pi lifecycle QA was removed: pinned pi-acp@0.0.34 lacks required close/fork capabilities. Obtain task-specific authorization before adding or running any real-provider QA. No provider credentials belong in source, test output, or fixture files.

`.tools/` contains an optional locally installed Pi ACP adapter and is ignored. `.qa/` is for ignored manual-test state/screenshots. Do not load their transient test settings into the user's normal agent configuration. Do not commit binaries or local histories.
