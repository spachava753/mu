# Mu

Mu is a minimal, Go-only native ACP desktop client. Keep the UI in MyGo's `ui` package. Do not introduce a web frontend, TypeScript tooling, or additional product features without a request.

Keep agent startup generic: execute the user-configured command without agent-specific defaults, adapter discovery, or runtime selection. The one exception is a command naming a built-in, `mu:<name>`, which the app runs as an `agent.Server` inside Mu instead of an executable; `agent.Process` hides the difference from everything else. Built-in adapters speak ACP over the transport they're given and don't care how they're hosted. Everything particular to a built-in lives in `internal/builtins`; the app and view have no conditions on which one it is, and neither has special handling for a harness's tools. Mu doesn't install agents or sign in for them. Saved agents are named command templates; existing threads retain their command snapshots. Session controls come only from the agent's advertised options, with legacy modes as a fallback. Do not invent model lists, context limits, or cost estimates.

## Greenfield development

Mu has no users depending on its existing data, and its data model is not settled. Do not version the schema or persistence format. Do not write migrations, backward-compatibility conversions, or automatic old-format detection/reset logic.

When making a breaking change to the data model or persistence layer, wipe the existing Mu-owned local data as part of the development task, with Mu stopped and its process lock respected. Do this directly, not through application migration/reset code. This includes disposable local QA state. Never delete agent-owned sessions or workspace files as part of this cleanup. Tests must use isolated data directories.

## Structure

- `main.go`: CLI flags and window startup/shutdown.
- `mygo.json`, `resources/`, `.github/workflows/release.yml`: packaging and releases through `go tool mygo build`. To release, bump `version` in `mygo.json`, commit, and push a matching tag (`v` plus the version). That tests on Linux and macOS and uploads to a draft release, which still has to be published. Never move a pushed tag, because Go's module proxy treats versions as permanent: bump to a new version instead. Keep the update signing key out of the repository.
- `internal/shellpath`: adopts the login shell's `PATH` when macOS starts Mu from the Dock or Finder, before anything looks up a command.
- `internal/desktop`: native view; use `ui.NewTester` for interaction tests.
- `internal/app`: synchronized thread state, ACP lifecycle, shared agent connections (`connections.go`), queued prompts (`queue.go`), projects and thread folders, permission handling, and persistence orchestration.
- `internal/agent`: ACP connection and the agent behind it, a child process or a registered in-process server.
- `internal/builtins`: what's particular to each built-in agent, given to `app.Open`.
- `internal/piacp`: built-in ACP adapter for Pi, driving `pi --mode rpc`. One Pi process per open session. Tests use `testdata/fakepi`; `TestRealPi` needs `MU_TEST_REAL_PI` and a fake model server only.
- `internal/media`: reads attached files and converts them to and from ACP content, with no UI or app state. It never refuses a file for its type: that's the agent's to report.
- `internal/textdiff`: bounded line diffs. The app computes them once when a tool call changes, and the view only draws them.
- `internal/procgroup`: process-group start and termination.
- `internal/workspace`: reads a folder's git state and pull request through the `git` and `gh` commands, with no UI or app state. Tests must never reach the real `gh`.
- `internal/worktree`: lists, plans, creates and removes a repository's worktrees through the `git` and `gh` commands, with no UI or app state. One `Resolve` serves the preview and the creation. It never writes when planning, and git runs with argument lists. Tests must never reach the real `gh`.
- `internal/state`: session references and settings with an exclusive process lock and atomic saves. Never persist conversations or SDK session state.
- `internal/agent/testdata/agent`: deterministic stdio agent used by tests.

Read `docs/behavior.md` and `GLOSSARY.md` for deliberate semantics, and record new behavior in `docs/behavior.md`. `README.md` is only for someone deciding whether to use Mu and how to install it: keep it short, and keep implementation details out of it. Require ACP v1 list/load/close/fork at initialization (not resume, which Mu never calls); reject missing capabilities without fallbacks. Fork uses the SDK's native `session/fork`, which ACP still marks unstable. Remove is local only; agent processes are not sandboxed.

Use MyGo's animation clock and transitions, not sleeps or per-frame timers. Respect reduced motion; never delay streamed text for presentation. Keep the empty overlay layer in `View.Draw`: it supplies a stable parent for overlay enter/exit transitions. Headless interaction tests advance their clock to settle motion; animation tests must also inspect intermediate frames.

The UI is main-thread-only and reads immutable snapshots. Nothing it calls may wait on a process or the network: checks of built-in agents run on the app's goroutines, and their results arrive in later snapshots. `Snapshot` runs for every frame, so work it would repeat is done once when data arrives. Background work changes application state under its mutex and signals a coalesced change channel. Never call blocking process teardown while holding the app mutex. Track detached teardown work in the app wait group so shutdown joins it. Close agent sessions before terminating processes; reconnects must wait for the close barrier, including after removal and re-import under a new local thread ID. Native fork is blocking and must run off the UI thread. A stopping turn must not release its busy state before its cancellation dispatch completes.

One agent connection (process) per agent configuration — saved agent ID plus command snapshot — serves every thread's session; route notifications and permissions by session ID. Discovery lists on that same connection. Do not start and stop processes needlessly: a saved agent's process keeps running while idle until the app closes or that saved configuration is edited or removed; other configurations (one-off commands, older snapshots) terminate with their last user. Every `acquire` must be balanced by a release, usually through `detachLocked`. Do not advertise or implement the ACP v1 client file system or terminal methods; ACP v2 removes them, and agents handle files and commands themselves. Process startup is independent of any one waiting thread. A context deadline cannot interrupt a blocked pipe write: hard deadlines use `Process.Bounded`, which kills the process on timeout; otherwise `Process.Close` lets the agent exit gracefully. Take connections out of use only through `retireLocked`: a configuration's replacement process must not start until the previous one has exited. An error response does not end a session; Stop escalates from `session/cancel` to `session/close` before terminating anything.

New threads get a randomly named folder in the general working directory, use a project's existing folder, or use a worktree of its repository. Never delete thread or project folders except Mu's own still-empty folder when thread creation fails, and a linked worktree the user asked to delete. That goes through `git worktree remove` after `RemovalCheck`, and `--force` only where git refuses any worktree with submodules and the check has passed. Never delete a branch except through `git branch -d` when asked. A thread's worktree is read from git, not saved. Worktree operations run on the app's goroutines and report through `WorktreeOperation`. `worktree.Add` and removal take turns across the app, in order; fetching and readying a worktree stay outside the line, so that a stalled remote holds up only its own operation. Deleting holds the affected threads as configuring, reserves the folder against new users, and waits for their sessions to close before removing it. Steps git can't safely be killed in use `settling`, and git runs in its own process group so that ending it ends its hooks and ssh. Tests pass a temporary default working directory to `app.Open`; never let them use the real one.

A draft's files go after its text, the common form across agents: Pi's RPC and its message pipeline put images after text whatever order they're given in. `SendParts` takes ordered parts, and an agent's replay decides the order shown after a reload. Files are read off the UI thread when attached. Do not gate attachments on prompt capabilities, or on what a model takes.

The app holds each thread's queue and starts the next prompt under the mutex as a turn ends, so a thread never looks idle in between. Steering interrupts as `Stop` does and sends only once the turn has ended: don't combine prompts or send one into a running turn. Queued prompts are memory only.

Process session notifications even while idle. Buffer setup notifications and replace the in-memory projection only after successful load/replay. Connection generations reject stale process notifications. Never inject transcript context. Group message chunks by agent message ID when present; do not send client message IDs. Project tool calls structurally and derive their visible text; updates patch only supplied fields. Discovery follows pagination, and never creates sessions or auto-imports them. Only complete listings reconcile metadata; absence is not proof of deletion. Scope session IDs to the agent configuration and command snapshot. Nested SDK session values in snapshots must be deep-copied. Mu uses `github.com/spachava753/acp-sdk`; do not switch to a different ACP SDK. `internal/agent/transport.go` delivers `session/update` notifications itself, in wire order, because SDK v0.6.0 does not order them.

Known tech debt, including that SDK gap and what to change once it is fixed, is tracked in [docs/tech-debt.md](docs/tech-debt.md).

## Checks

For Mu UI changes, visual regressions, or screenshot/animation QA requests, read and follow [mu-visual-qa](.bb/skills/mu-visual-qa/SKILL.md). Capture with the offline harness and inspect the images; passing tests alone is not visual QA.

**Never change a UI test silently.** The tests in `internal/desktop`, above all the workflows in `workflows_test.go` and the scenarios named in the QA skill, record the behavior and appearance the maintainer wants preserved. A change to one means part of the UI's surface changed, on purpose or not, and the maintainer needs to come and look.

- If one fails during other work, treat it as a regression in the code first. Don't edit the test to make it pass unless the task really does change that behavior.
- Whenever you modify, weaken, skip or remove one, say so loudly: in its own section at the top of your final message, and in the commit message. Name each test, what it checked before and what it checks now, why it changed, and what a person will see differently. Don't bury this in a summary or leave it to the diff.
- Mention new tests too, but they don't need this. What matters is any change to what an existing test holds in place, including its helpers and fixtures.

Run `go test -race ./...`, `go vet ./...`, and `go build -o bin/mu .` after meaningful changes. Tests use an offline fixture with its own session store, separate from Mu’s data directory. The old Pi lifecycle QA was removed: pinned pi-acp@0.0.34 lacks required close/fork capabilities. Obtain task-specific authorization before adding or running any real-provider QA, including `TestLivePi` in `internal/desktop`, which drives the Pi installed on the machine against a real model. No provider credentials belong in source, test output, or fixture files.

An installed Mu may be running, and it shares the default data directory and its exclusive lock, so plain `go run .` can refuse to start. Run development copies with `go run . --data-dir "$PWD/.qa/dev"`, and don't stop the installed Mu to make room.

`.tools/` contains an optional locally installed Pi ACP adapter and is ignored. `.qa/` is for ignored manual-test state/screenshots. Do not load their transient test settings into the user's normal agent configuration. Do not commit binaries or local histories.
