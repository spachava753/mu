# Mu

A small ACP desktop client, written entirely in Go with [MyGo](https://github.com/egoist/mygo)'s native `ui` package. No TypeScript, HTML, webview, or frontend build step.

- Create independent threads using any locally installed **stdio ACP v1 agent command**.
- Stream replies as selectable Markdown; inspect thinking, tool results, and terminal output.
- Answer agent permission requests, or stop a running turn.
- Archive/restore, delete, and fork threads.
- Keep local history across restarts; resume the agent's session when it supports `session/load`.

This is intentionally a single-window, text-chat client. Configure authentication, models, and agent-specific settings with the agent's own CLI. It does not provide remote ACP transports, attachment input, MCP configuration, a model picker, or workspace management.

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

Choose **New thread**, enter the agent command and an existing working directory, then **Create thread**. Mu has no built-in agent default; it remembers your last command or uses the `-agent` flag. The agent starts on the first message. Threads have separate agent processes, so multiple threads can work at once.

Agent commands are executable names plus arguments. Quotes preserve spaces; there is **no shell expansion**, redirection, or automatic `~`/`$VARIABLE` expansion. Use absolute paths, a wrapper executable, or `env NAME=value agent …` when needed. Do not put secrets in the command: the command is saved with the thread.

Optional startup flags prefill the form:

```sh
./bin/mu -agent 'my-agent --acp' -cwd /absolute/path/to/project
./bin/mu -data-dir /absolute/path/to/isolated-history
```

**Enter** sends a message; **Shift+Enter** inserts a newline. You can also click **Send**. **Stop** requests ACP cancellation, then terminates an unresponsive agent after two seconds. Stop an active turn before archiving, deleting, or forking it. Drafts survive switching threads within the app, but are not saved across app restarts.

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

Tested here with Pi **1.3.0** and `openai/gpt-6-astra`. The adapter sometimes turns a provider/model error into an empty successful turn. Mu displays a warning when a turn ends without any response; check the Pi CLI's login and model settings if this happens.

## Thread semantics

- **Archive** hides a thread from the active list and stops its idle agent process. **Show archived → Restore** makes it writable again.
- **Delete** requires confirmation and removes Mu's local copy only. It does **not** remove workspace files or the agent's own saved sessions.
- **Fork** creates an independent thread with a copy of the visible transcript. On its first message, that transcript is supplied once as historical context to a fresh ACP session. This works even with agents that lack native forking. It does **not** clone hidden agent state, roll back files, or replay earlier tool calls. The UI makes this distinction explicit.
- After a restart or archive, agents advertising `session/load` resume the saved session. Replay notifications do not duplicate Mu's transcript. If loading fails, Mu reports the error instead of silently starting over; use **Fork** to continue with transcript context.
- Agents without session loading continue in a fresh session with the visible transcript supplied once, with a notice. Large transcripts consume model context; Mu does not silently summarize or truncate them.

## Local data and trust

History lives in `$XDG_DATA_HOME/mu/threads.json`, or `~/.local/share/mu/threads.json`. Writes use a private temporary file and atomic replacement; an exclusive lock prevents two app instances from overwriting the same history. Streaming updates are saved every half-second and at turn completion. Corrupt or unknown-version history causes startup to fail without overwriting it. Use `-data-dir` for an isolated test instance.

**Mu is not a sandbox.** Agent executables inherit your environment and run with your user permissions. Only run agents and commands you trust. Permission controls answer requests the agent actually sends; Mu cannot force every agent tool to request approval. Client-delegated file reads/writes are confined to the thread's workspace (including symlink checks), with an 8 MiB text limit. Delegated terminal commands run as your user, with output capped at 1 MiB per terminal. On Unix, cancellation/cleanup terminates process groups; on Windows only direct child termination is implemented.

No telemetry or automatic network calls are made by Mu itself. Agent processes may use their own network services. Local transcripts, commands, tool results, and error messages can contain sensitive data; history is not encrypted.

## Check

```sh
go test -race ./...
go vet ./...
go build -o bin/mu .
```

The normal suite needs no credentials or model calls. It includes a real stdio fixture process, persistence/recovery, load and portable fork behavior, permissions, graceful and forced cancellation, agent failures, filesystem confinement, terminal lifecycle, and MyGo headless interaction tests at 1100×780 and 800×600.

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
