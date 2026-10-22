# Mu

Mu is a minimal, Go-only native ACP desktop client. Keep the UI in MyGo's `ui` package. Do not introduce a web frontend, TypeScript tooling, or additional product features without a request.

Keep agent startup generic: execute the user-configured command without agent-specific defaults, adapter discovery, or runtime selection. Pi installation and launch configuration belong in test setup and documentation, not client startup logic. Saved agents are named command templates; existing threads retain their command snapshots. Session controls come only from the agent's advertised options, with legacy modes as a fallback. Do not invent model lists, context limits, or cost estimates.

## Greenfield development

Mu has no users depending on its existing data, and its data model is not settled. Do not version the schema or persistence format. Do not write migrations, backward-compatibility conversions, or automatic old-format detection/reset logic.

When making a breaking change to the data model or persistence layer, wipe the existing Mu-owned local data as part of the development task, with Mu stopped and its process lock respected. Do this directly, not through application migration/reset code. This includes disposable local QA state. Never delete agent-owned sessions or workspace files as part of this cleanup. Tests must use isolated data directories.

## Structure

- `main.go`: CLI flags and window startup/shutdown.
- `internal/desktop`: native view; use `ui.NewTester` for interaction tests.
- `internal/app`: synchronized thread state, ACP lifecycle, permission handling, and persistence orchestration.
- `internal/agent`: stdio ACP connection and process/filesystem/terminal services.
- `internal/state`: session references and settings with an exclusive process lock and atomic saves. Never persist conversations or SDK session state.
- `internal/agent/testdata/agent`: deterministic stdio agent used by tests.

Read `README.md` and `GLOSSARY.md` for deliberate semantics. Require ACP v1 list/load/resume/close/fork at initialization; reject missing capabilities without fallbacks. Fork uses the SDK’s unstable native method. Remove is local only; agent processes are not sandboxed.

Use MyGo's animation clock and transitions, not sleeps or per-frame timers. Respect reduced motion; never delay streamed text for presentation. Keep the empty overlay layer in `View.Draw`: it supplies a stable parent for overlay enter/exit transitions. Headless interaction tests advance their clock to settle motion; animation tests must also inspect intermediate frames.

The UI is main-thread-only and reads immutable snapshots. Background work changes application state under its mutex and signals a coalesced change channel. Never call blocking process teardown while holding the app mutex; terminal callbacks acquire it. Track detached teardown work in the app wait group so shutdown joins it. Close agent sessions before terminating processes; reconnects must wait for the close barrier, including after removal and re-import under a new local thread ID. Native fork is blocking and must run off the UI thread. A stopping turn must not release its busy state before its cancellation dispatch completes.

Process session notifications even while idle. Buffer setup notifications and replace the in-memory projection only after successful load/replay. Connection generations reject stale process notifications. Never inject transcript context. Discovery initializes/lists on a separate connection, follows pagination, and never creates sessions or auto-imports them. Only complete listings reconcile metadata; absence is not proof of deletion. Scope session IDs to the agent configuration and command snapshot. Nested SDK session values in snapshots must be deep-copied. `internal/agent/capabilities.go` bridges the boolean-option capability absent from SDK v0.13.5; remove it once the SDK provides the typed field.

## Checks

For Mu UI changes, visual regressions, or screenshot/animation QA requests, read and follow [mu-visual-qa](.bb/skills/mu-visual-qa/SKILL.md). Capture with the offline harness and inspect the images; passing tests alone is not visual QA.

Run `go test -race ./...`, `go vet ./...`, and `go build -o bin/mu .` after meaningful changes. Tests use an offline fixture with its own session store, separate from Mu’s data directory. The old Pi lifecycle QA was removed: pinned pi-acp@0.0.34 lacks required resume/close/fork capabilities. Obtain task-specific authorization before adding or running any real-provider QA. No provider credentials belong in source, test output, or fixture files.

`.tools/` contains an optional locally installed Pi ACP adapter and is ignored. `.qa/` is for ignored manual-test state/screenshots. Do not load their transient test settings into the user's normal agent configuration. Do not commit binaries or local histories.
