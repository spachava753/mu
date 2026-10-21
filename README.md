# Mu

A small ACP desktop client, written entirely in Go with [MyGo](https://github.com/egoist/mygo)'s native `ui` package. No TypeScript, HTML, webview, or frontend build step.

- Create independent threads using any locally installed **stdio ACP v1 agent command**.
- Save named agents and a default in **Settings**; reuse them when creating threads.
- Collapse the thread sidebar; hover and selected-thread highlights remain distinct.
- Use a matte, native interface that follows the desktop's light/dark appearance, with short transitions for panels, menus, and navigation.
- Stream replies as selectable Markdown; inspect thinking, plans, tool results, and terminal output.
- Change agent-reported session options and inspect context usage, limits, and cost.
- Answer agent permission requests, or stop a running turn.
- Archive/restore, delete, and fork threads.
- Keep local history across restarts; resume the agent's session when it supports `session/load`.

This is intentionally a single-window, text-chat client. Configure authentication and startup settings with the agent's own CLI. Model and other session controls appear only when the agent exposes them over ACP; Mu supplies no model catalog or provider defaults. It does not provide remote ACP transports, attachment input, MCP configuration, or workspace management.

## Run

Requires **Go 1.27.1+**. Native Linux windows need **GTK 3** and a graphical desktop; WebKitGTK is not needed. MyGo also supports macOS and Windows, but this version has been exercised on Linux only.

```sh
go run .
```

Or build a standalone executable:

```sh
go build -o bin/mu .
./bin/mu
```

Open **Settings** at the bottom of the sidebar to save an agent name and command, and choose a default. Then choose **New thread**, select a saved agent and an existing working directory, and **Create thread**. A custom one-off command is also supported. Mu has no built-in agent default; an existing installation's last-used command is migrated into a saved configuration once. Editing or removing a saved agent never changes existing threads' command snapshots.

The agent starts on the first message or when you open **Session options**. Threads have separate agent processes, so multiple threads can work at once. The sidebar toggle preserves its collapsed state across restarts; Settings and New thread remain available in the collapsed rail.

Agent commands are executable names plus arguments. Quotes preserve spaces; there is **no shell expansion**, redirection, or automatic `~`/`$VARIABLE` expansion. Use absolute paths, a wrapper executable, or `env NAME=value agent …` when needed. Do not put secrets in the command: the command is saved with the thread.

Optional startup flags prefill the form:

```sh
./bin/mu -agent 'my-agent --acp' -cwd /absolute/path/to/project
./bin/mu -data-dir /absolute/path/to/isolated-history
```

**Enter** sends a message; **Shift+Enter** inserts a newline. You can also click **Send**. **Stop** requests ACP cancellation, then terminates an unresponsive agent after two seconds. Stop an active turn before archiving, deleting, or forking it. Drafts survive switching threads within the app, but are not saved across app restarts.

## Session options and updates

Open **Session options** in a thread to connect and discover its controls without sending a prompt. Mu renders the agent's select options (including groups) and boolean options, and uses legacy modes only when configuration options are absent. Successful changes replace the full advertised option list, so dependent controls stay in sync. Rejected changes leave the acknowledged values intact and show the agent's error. Agents that expose no options get an explicit empty state.

The composer keeps a **context-usage indicator** visible, with the reported token count, window limit, percentage, and progress bar. Reported **session cost** appears alongside it in the agent's currency. Click the indicator for last-turn input/output, thinking and cache counts, and the stop reason; these are no longer buried in Session options. Missing usage is explicitly shown as not reported, never zero. Provider-specific limits and other `_meta` data remain inspectable under **Agent metadata**, without guessing their meaning.

Type **/** at the start of the draft for a scrollable command picker above the composer. It filters the agent's advertised commands as you type. **Up/Down** selects, **Enter/Tab** inserts, and **Escape** dismisses. Clicking a command also inserts it and returns focus to the draft. Completion never sends a prompt; add arguments and send separately. On a new thread, typing `/` connects to discover commands without sending a prompt.

Long model/option lists scroll within a bounded menu rather than extending past the window. Keyboard selection scrolls the highlighted choice into view. Repeated display prefixes, such as “Thinking: high” underneath a Thinking label, are shortened to “high”; protocol values are unchanged.

The sidebar resizes over 220 ms, highlights fade over 120 ms, and menus/dialogs/screens enter and leave over 160 ms. Disclosures expand smoothly, context bars ease toward new reported usage, and an activity spinner identifies an outstanding request. Streamed text is not delayed for a typewriter effect. Motion respects the desktop's reduced-motion preference.

Session updates are processed while idle as well as during prompts. Mu handles user/assistant/thought chunks, tool calls and partial updates, legacy and ID-addressed plans/removals, available commands, current modes, configuration changes, session info, and usage updates exposed by the ACP SDK. Non-text content is retained with a readable placeholder and inspectable protocol details. Setup notifications are buffered; conversation replay from `session/load` does not duplicate history. Unsupported experimental capabilities, such as compaction, are not advertised.

## Pi

The tested adapter is [`pi-acp@0.0.34`](https://github.com/svkozak/pi-acp). It bridges ACP to the **installed Pi CLI**; it is not bundled into Mu and does not add JavaScript to Mu's UI.

A pinned local installation was made in this checkout's ignored `.tools/` directory. To reproduce it:

```sh
npm install --prefix .tools --ignore-scripts --no-audit --no-fund pi-acp@0.0.34
```

Configure the local adapter explicitly, just like any other ACP agent. Mu does not search for adapters, select runtimes, or rewrite commands:

```sh
./bin/mu -agent "node $PWD/.tools/node_modules/pi-acp/dist/index.js"
```

Create a new thread with that command. Existing threads keep their saved command; a thread configured as bare `pi-acp` requires that executable on `PATH`.

`node` and `pi` must be on `PATH`, and Pi must already be signed in. The adapter uses Pi's configured provider/model. To override Pi's invocation, point `PI_ACP_PI_COMMAND` at a wrapper executable. For example, a wrapper can run `pi --model YOUR_PROVIDER/YOUR_MODEL --thinking low "$@"`. Do not use the testing wrapper in `.tools/` for normal coding: it intentionally disables tools.

The adapter deliberately sends a startup listing (Pi version, context files, skills, prompts, extensions) as an ACP assistant message after creating a session. This is adapter-generated information, not a model response or a required ACP banner. Mu displays it because the adapter labels it as ordinary agent text; other agents can choose not to send one.

To suppress that listing, merge `"quietStartup": true` into Pi's project `.pi/settings.json` or user `~/.pi/agent/settings.json` (or the agent directory selected by `PI_CODING_AGENT_DIR`). Project settings override user settings. This also affects Pi's terminal startup display, and the adapter may still send an update notice. Mu does not modify these settings or filter Pi messages specially.

Tested here with Pi **1.3.0** and `openai/gpt-6-astra`. The adapter sometimes turns a provider/model error into an empty successful turn. Mu displays a warning when a turn ends without any response; check the Pi CLI's login and model settings if this happens.

## Thread semantics

- **Archive** hides a thread from the active list and stops its idle agent process. **Show archived → Restore** makes it writable again.
- **Delete** requires confirmation and removes Mu's local copy only. It does **not** remove workspace files or the agent's own saved sessions.
- **Fork** creates an independent thread with a copy of the visible transcript. On its first message, that transcript is supplied once as historical context to a fresh ACP session. This works even with agents that lack native forking. It does **not** clone hidden agent state, roll back files, or replay earlier tool calls. The UI makes this distinction explicit.
- After a restart or archive, agents advertising `session/load` resume the saved session. Replay notifications do not duplicate Mu's transcript. If loading fails, Mu reports the error instead of silently starting over; use **Fork** to continue with transcript context.
- Agents without session loading continue in a fresh session with the visible transcript supplied once, with a notice. Large transcripts consume model context; Mu does not silently summarize or truncate them.

## Local data and trust

History, saved agents, sidebar preferences, and reported session state live in `$XDG_DATA_HOME/mu/threads.json`, or `~/.local/share/mu/threads.json`. Writes use a private temporary file and atomic replacement; an exclusive lock prevents two app instances from overwriting the same history. Streaming updates are saved every half-second and at turn completion. Corrupt or unknown-version history causes startup to fail without overwriting it. Use `-data-dir` for an isolated test instance.

**Mu is not a sandbox.** Agent executables inherit your environment and run with your user permissions. Only run agents and commands you trust. Permission controls answer requests the agent actually sends; Mu cannot force every agent tool to request approval. Client-delegated file reads/writes are confined to the thread's workspace (including symlink checks), with an 8 MiB text limit. Delegated terminal commands run as your user, with output capped at 1 MiB per terminal. On Unix, cancellation/cleanup terminates process groups; on Windows only direct child termination is implemented.

No telemetry or automatic network calls are made by Mu itself. Agent processes may use their own network services. Local transcripts, commands, tool results, and error messages can contain sensitive data; history is not encrypted.

## Check

```sh
go test -race ./...
go vet ./...
go build -o bin/mu .
```

The normal suite needs no credentials or model calls. It includes a real stdio fixture process, settings migration/persistence, session configuration and updates, load and portable fork behavior, permissions, graceful and forced cancellation, agent failures, filesystem confinement, terminal lifecycle, and MyGo headless interaction tests at 1100×780 and 800×600. The UI tests exercise row highlights, sidebar collapse, saved agents, session option changes, 100-model menus, and slash completion with 80 commands using mouse and keyboard.

To save native-renderer QA screenshots in both sizes and themes:

```sh
MU_QA_RENDER_DIR="$PWD/.qa/session-ui" go test ./internal/desktop -run 'TestMatteSidebarAndSessionControls|TestAgentSettingsUI|TestLongModelOptionsScroll|TestSlashCommandPicker' -count=1
```

Motion tests sample intermediate frames, reversal, and reduced-motion behavior. To record an animated native-renderer demo (using only the offline fixture):

```sh
MU_QA_RENDER_DIR="$PWD/.qa/motion" go test ./internal/desktop -run '^TestRecordMotion$' -count=1
```

An **opt-in test that uses existing Pi authentication and sends four small provider requests** is also available after installing the adapter:

```sh
MU_QA_PI=1 go test -race ./internal/app -run '^TestQAPiLifecycle$' -v -timeout 5m
```

It uses isolated temporary workspace/session state, disables tools and resource discovery, and checks concurrent conversations, session resumption, fork context, archive/restore, and deletion. It currently selects the tested `openai/gpt-6-astra` model in the test wrapper, not in the application.

## Code

- `main.go`: startup and native window lifecycle.
- `internal/desktop`: MyGo views and headless interaction tests.
- `internal/app`: thread lifecycle and streamed update handling.
- `internal/agent`: ACP Go SDK integration, child processes, file and terminal requests.
- `internal/state`: atomic local storage and locking.

The protocol implementation uses [coder/acp-go-sdk](https://github.com/coder/acp-go-sdk).
