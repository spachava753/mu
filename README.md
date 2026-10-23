# Mu

A small ACP desktop client, written entirely in Go with [MyGo](https://github.com/egoist/mygo)'s native `ui` package. No TypeScript, HTML, webview, or frontend build step.

- Create independent threads using compatible locally installed **stdio ACP v1 agent commands**. Each thread works in its own new folder in a general **working directory**, or in a **project**'s shared folder.
- Save named agents and a default in **Settings**; reuse them when creating threads.
- Collapse the thread sidebar; hover and selected-thread highlights remain distinct.
- Use a matte, native interface that follows the desktop's light/dark appearance, with short transitions for panels, menus, and navigation.
- Stream replies as selectable Markdown; inspect thinking, plans, tool results, and terminal output.
- Change agent-reported session options and inspect context usage, limits, and cost.
- Answer agent permission requests, or stop a running turn.
- Discover agent sessions and explicitly import selected references, including sessions created in other clients.
- Archive/restore, remove references, and fork sessions through the agent.
- Rebuild conversations from agent-owned history across restarts; Mu does not persist transcripts.

This is intentionally a single-window, text-chat client. Configure authentication and startup settings with the agent's own CLI. Model and other session controls appear only when the agent exposes them over ACP; Mu supplies no model catalog or provider defaults. It does not provide remote ACP transports, attachment input, or MCP configuration.

## Required agent capabilities

Mu requires `loadSession: true` and `sessionCapabilities.list`, `.close`, and `.fork` in the initialization response, in addition to the baseline ACP methods. An agent missing any required capability is rejected with an error naming the missing methods. Mu does not compensate by saving transcripts, injecting context, or creating replacement sessions. Native forking uses the SDK’s currently unstable `session/fork` API and is part of this required contract. Agent-side deletion is not a Mu feature.

Mu currently speaks ACP v1. It restores sessions with `session/load`, which replays history; it does not use `session/resume`, which restores without replay, so it does not require it. ACP v2 is not yet supported.

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

Open **Settings** at the bottom of the sidebar to save an agent name and command, and choose a default. Then choose **New thread**, select a saved agent and optionally a project, and **Create thread**. A custom one-off command is also supported. Mu has no built-in agent default. Saved agents are created explicitly in Settings. Editing or removing a saved agent never changes existing threads' command snapshots.

## Working directory and projects

Without a project, each new thread gets its own randomly named folder (such as `2026-10-21-3fa9c2`) in the general **working directory**, `~/mu` unless you choose another absolute path in **Settings**. Mu creates the folder when it creates the thread. Changing the working directory affects only new threads.

A **project** is a name for an existing folder. Every thread created in a project works directly in that folder, so its threads share files. Add, edit, and remove projects in **Settings**, and choose one under **Project** when creating a thread; a new thread starts in the project of the thread you were viewing. The sidebar groups threads under their projects once a project exists. Imported sessions whose working directory is a project's folder join that project. Removing a project removes only Mu's entry: its threads keep their folder and session, and Mu never creates or deletes a project folder. Mu never deletes thread folders either.

## Agent connections

For a new thread, the agent starts on the first message or when you open **Session options**. Opening a saved session starts its agent and loads history automatically, including the selected thread at application startup.

Threads that use the same agent configuration share one agent process, as ACP allows: each thread is a separate session on that connection, and sessions can work at the same time. The process starts in the working directory and stops when its last session closes. If it exits, its threads reconnect on a new process when next used. Threads with different configurations use different processes.

Each sidebar row shows the thread's agent, its folder when it is not a project or Mu-created folder, and last activity, or its most urgent state (working, needs permission, agent error). The sidebar toggle preserves its collapsed state across restarts; New thread, Import sessions, and Settings remain available in the collapsed rail.

Agent commands are executable names plus arguments. Quotes preserve spaces; there is **no shell expansion**, redirection, or automatic `~`/`$VARIABLE` expansion. Use absolute paths, a wrapper executable, or `env NAME=value agent …` when needed. Do not put secrets in the command: the command is saved with the thread.

Optional startup flags prefill the form or isolate Mu's data:

```sh
./bin/mu -agent 'my-agent --acp'
./bin/mu -data-dir /absolute/path/to/isolated-history
```

**Enter** sends a message; **Shift+Enter** inserts a newline. You can also click **Send**. **Stop** requests ACP cancellation. If the agent has not ended the turn ten seconds later, giving it time to finish a tool call or clean up, including when it has stopped reading Mu's messages, Mu terminates the agent process, which also disconnects other sessions on that process. Stop an active turn before archiving, removing, or forking it. Drafts survive switching threads within the app, but are not saved across app restarts.

## Session options and updates

The composer toolbar shows the agent's advertised select options, such as model and reasoning level, as compact menus, so the current values are always visible. The **Session options** button beside them connects and discovers the controls without sending a prompt, and opens a dialog with every option, including boolean options, descriptions, and agent metadata. Mu renders the agent's select options (including groups) and boolean options, and uses legacy modes only when configuration options are absent. Successful changes replace the full advertised option list, so dependent controls stay in sync. Rejected changes leave the acknowledged values intact and show the agent's error. Agents that expose no options get an explicit empty state.

The composer keeps a **context-usage indicator** visible, with the reported token count, window limit, percentage, and a bar that turns amber at 70% and red at 90%. Reported **session cost** appears alongside it in the agent's currency. Click the indicator for last-turn input/output, thinking and cache counts, and the stop reason; these are no longer buried in Session options. Missing usage is explicitly shown as not reported, never zero. Provider-specific limits and other `_meta` data remain inspectable under **Agent metadata**, without guessing their meaning.

Type **/** at the start of the draft for a scrollable command picker above the composer. It filters the agent's advertised commands as you type. **Up/Down** selects, **Enter/Tab** inserts, and **Escape** dismisses. Clicking a command also inserts it and returns focus to the draft. Completion never sends a prompt; add arguments and send separately. On a new thread, typing `/` connects to discover commands without sending a prompt.

Long model/option lists scroll within a bounded menu rather than extending past the window. Keyboard selection scrolls the highlighted choice into view. Repeated display prefixes, such as “Thinking: high” underneath a Thinking label, are shortened to “high”; protocol values are unchanged.

The sidebar resizes over 220 ms, highlights fade over 120 ms, and menus/dialogs/screens enter and leave over 160 ms. Disclosures expand smoothly, context bars ease toward new reported usage, and an activity spinner identifies an outstanding request. Streamed text is not delayed for a typewriter effect. Motion respects the desktop's reduced-motion preference.

Session updates are processed while idle as well as during prompts. Mu handles user/assistant/thought chunks, grouped into messages by the agent's message IDs when it sends them (otherwise adjacent chunks of the same kind form one message); tool calls and their partial updates, which change only the fields they include; legacy and ID-addressed plans/removals, available commands, current modes, configuration changes, session info, and usage updates exposed by the ACP SDK. Non-text content is retained with a readable placeholder and inspectable protocol details. Setup notifications are buffered; a successful `session/load` replaces the in-memory conversation with the replay, and session options or modes in the load response take precedence over older values replayed before it. A failed load leaves the old view unchanged and displays an error, rather than exposing a partial replay as complete history. Unsupported experimental capabilities, such as compaction, are not advertised.

## Pi compatibility

The previously tested `pi-acp@0.0.34` adapter is **not compatible with this session policy**. It advertises list/load (and delete), but not close/fork. Mu rejects it at initialization. Upgrade to an adapter that advertises and implements the required methods; Mu does not special-case Pi or supply missing capabilities. Pi's `_meta.terminal_output` data is kept as inspectable metadata, not shown as terminal output.

The optional installation in `.tools/` remains ignored and is not bundled into Mu. Earlier real-provider QA was for the old persistence model; that obsolete test has been removed. Current checks use an offline, stateful ACP fixture and do not use Pi credentials.

## Discovery and thread semantics

Choose **Import sessions** in the sidebar, select a saved agent, then **Find sessions**. Discovery follows the agent's pagination across all exposed directories on a separate process started in the working directory, without creating/loading sessions or sending prompts. Filter by title, directory, or session ID, select rows, and choose **Add selected to Mu**. Nothing is selected or imported automatically. History loads only when an imported thread is opened.

- Session identity is scoped to the saved configuration ID and its command snapshot, not just the session ID or agent display name. Editing a command cannot silently retarget existing references. Mu cannot infer that two different configurations share a session store, or detect an external account/storage change hidden behind an unchanged command.
- Refreshing discovery updates cached titles/activity for managed references. A reference missing from a complete listing is marked **not listed**, not deleted. Failed or incomplete listings do not reconcile references.
- A session's working directory cannot change in ACP. If a listing reports a different one, Mu shows a notice and suggests re-importing. Additional directories are supplied on each load: if a listing reports different ones, Mu keeps loading the session with the directories accepted at import and shows a notice. Listings never widen file access.
- **Fork** and **Archive** are buttons in the thread header. **Reload history** (or **Retry connection**) and **Remove from Mu…** are in its **More thread actions** menu; Archive and Remove are also in a thread row's context menu.
- A load error keeps the existing reference and shows **Retry connection** beside the error. Mu never creates a replacement session automatically. **Reload history** closes the connection and replaces the view with a fresh replay, including external changes.
- **Archive** hides a thread from the active list and closes its idle agent session. **Show archived → Restore** loads it again.
- **Remove from Mu** requires confirmation and removes only Mu's reference. It does not call agent-side deletion or remove workspace files. The session can be imported again.
- **Fork** sends ACP `session/fork` on the source thread's connection, connecting it first if needed; the agent creates the fork. The source stays connected and unchanged. The fork response contains no history, so Mu closes the fork, saves the new reference, and loads its history from the agent on the same connection when it is opened. No transcript is copied or supplied as a prompt; workspace files are not rolled back.
- Archiving, removing, reloading, and shutdown request `session/close` for a live session; its process stops when no other session uses it. A close failure is surfaced. If the agent does not answer within ten seconds, Mu terminates the process so cleanup cannot hang, which also disconnects other sessions on it.
- Another client can still use an imported session. Mu prevents conflicting local operations on the same managed reference, but does not promise cross-client locking.

## Local data and trust

Session references, cached titles/activity, workspace roots, saved agents, projects, the working directory, and sidebar/archive preferences live in `$XDG_DATA_HOME/mu/threads.json`, or `~/.local/share/mu/threads.json`. The unversioned local data contains no messages, thinking, tools, usage, session options, raw metadata, or running-turn state. Those exist only in memory and are reconstructed from the agent.

Writes use a private temporary file and atomic replacement; an exclusive lock prevents two instances from overwriting the same registry. Metadata changes are saved periodically and at lifecycle boundaries. Invalid data causes startup to fail without overwriting it. Use `-data-dir` for an isolated test instance.

Mu is a greenfield project with no established data model and no users depending on existing data. Persistence is not versioned. Breaking data-model or persistence changes require wiping existing Mu-owned local data during development, not adding migrations, compatibility code, or automatic startup resets. Agent-owned sessions and workspace files are never part of that wipe; use **Import sessions** to add session references again.

**Mu is not a sandbox.** Agent executables inherit your environment and run with your user permissions. Only run agents and commands you trust. Permission controls answer requests the agent actually sends; Mu cannot force every agent tool to request approval. Client-delegated file reads/writes are confined to the requesting session's roots: its working directory plus the additional directories it was loaded with (including symlink checks), with an 8 MiB text limit. Sessions sharing a process cannot use each other's roots. Delegated terminal commands run as your user, with output capped at 1 MiB per terminal. On Unix, cancellation/cleanup terminates process groups; on Windows only direct child termination is implemented.

No telemetry or automatic network calls are made by Mu itself. Agent processes may use their own network services. Saved commands, titles, and workspace paths can contain sensitive data; the local registry is not encrypted. Agent-owned histories follow the agent’s own storage policy.

## Check

```sh
go test -race ./...
go vet ./...
go build -o bin/mu .
```

The normal suite needs no credentials or model calls. It includes a real stdio fixture process, settings persistence, session configuration and updates, agent replay and native fork behavior, paginated discovery/selective import, external deletion, permissions, graceful and forced cancellation, agent failures, filesystem confinement, terminal lifecycle, and MyGo headless interaction tests at 1100×780 and 800×600. The UI tests exercise row highlights, sidebar collapse, saved agents, session option changes, 100-model menus, and slash completion with 80 commands using mouse and keyboard.

To save native-renderer QA screenshots in both sizes and themes:

```sh
MU_QA_RENDER_DIR="$PWD/.qa/session-ui" go test ./internal/desktop -run 'TestMatteSidebarAndSessionControls|TestAgentSettingsUI|TestLongModelOptionsScroll|TestSlashCommandPicker' -count=1
```

Motion tests sample intermediate frames, reversal, and reduced-motion behavior. To record an animated native-renderer demo (using only the offline fixture):

```sh
MU_QA_RENDER_DIR="$PWD/.qa/motion" go test ./internal/desktop -run '^TestRecordMotion$' -count=1
```

## Code

- `main.go`: startup and native window lifecycle.
- `internal/desktop`: MyGo views and headless interaction tests.
- `internal/app`: thread lifecycle and streamed update handling.
- `internal/agent`: ACP Go SDK integration, child processes, file and terminal requests.
- `internal/state`: session references, atomic storage and locking.
- `GLOSSARY.md`: session ownership and discovery/import terminology.

The protocol implementation uses [coder/acp-go-sdk](https://github.com/coder/acp-go-sdk).
