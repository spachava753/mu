# Mu

Mu is a minimal, Go-only native ACP desktop client. Keep the UI in MyGo's `ui` package. Do not introduce a web frontend, TypeScript tooling, or additional product features without a request.

Keep agent startup generic: execute the user-configured command without agent-specific defaults, adapter discovery, or runtime selection. Pi installation and launch configuration belong in test setup and documentation, not client startup logic. Saved agents are named command templates; existing threads retain their command snapshots. Session controls come only from the agent's advertised options, with legacy modes as a fallback. Do not invent model lists, context limits, or cost estimates.

## Structure

- `main.go`: CLI flags and window startup/shutdown.
- `internal/desktop`: native view; use `ui.NewTester` for interaction tests.
- `internal/app`: synchronized thread state, ACP lifecycle, permission handling, and persistence orchestration.
- `internal/agent`: stdio ACP connection and process/filesystem/terminal services.
- `internal/state`: versioned JSON history with an exclusive process lock and atomic saves.
- `internal/agent/testdata/agent`: deterministic stdio agent used by tests.

Read `README.md` for deliberate semantics: forks copy visible transcript context, not agent-internal state; delete is local only; agent processes are not sandboxed.

Use MyGo's animation clock and transitions, not sleeps or per-frame timers. Respect reduced motion; never delay streamed text for presentation. Keep the empty overlay layer in `View.Draw`: it supplies a stable parent for overlay enter/exit transitions. Headless interaction tests advance their clock to settle motion; animation tests must also inspect intermediate frames.

The UI is main-thread-only and reads immutable snapshots. Background work changes application state under its mutex and signals a coalesced change channel. Never call blocking process teardown while holding the app mutex; terminal callbacks acquire it. Track detached teardown work in the app wait group so shutdown joins it. A stopping turn must not release its busy state before its cancellation dispatch completes.

Process session notifications even while idle. Buffer setup notifications and suppress conversation replay, not configuration/usage updates. Connection generations reject stale process notifications. Preserve pending transcript bootstrap across discovery-only connections and restarts. Nested SDK session values in snapshots must be deep-copied. `internal/agent/capabilities.go` bridges the boolean-option capability absent from SDK v0.13.5; remove it once the SDK provides the typed field.

## Checks

Run `go test -race ./...`, `go vet ./...`, and `go build -o bin/mu .` after meaningful changes. Tests are offline by default. `MU_QA_PI=1` opts into a test using existing Pi credentials and real model requests; obtain task-specific authorization before running it. No provider credentials belong in source, test output, or fixture files.

`.tools/` contains an optional locally installed Pi ACP adapter and is ignored. `.qa/` is for ignored manual-test state/screenshots. Do not load their transient test settings into the user's normal agent configuration. Do not commit binaries or local histories.
