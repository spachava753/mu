# Behavior

What Mu does and deliberately does not do. Terms are defined in [GLOSSARY.md](../GLOSSARY.md).

This is intentionally a single-window, text-chat client. Configure authentication and startup settings with the agent's own CLI. Model and other session controls appear only when the agent exposes them over ACP; Mu supplies no model catalog or provider defaults. It does not provide remote ACP transports, attachment input, or MCP configuration.

## Getting started

Choose **New thread**, select an agent and optionally a project, and **Create thread**. If [Pi](#pi) is installed it's ready to use with nothing to set up. For any other ACP agent, open **Settings** at the bottom of the sidebar to save a name and command, and choose a default; a custom one-off command is also supported. Editing or removing a saved agent never changes existing threads' command snapshots.

## Working directory and projects

Without a project, each new thread gets its own randomly named folder (such as `2026-10-21-3fa9c2`) in the general **working directory**, `~/mu` unless you choose another absolute path in **Settings**. Mu creates the folder when it creates the thread. Changing the working directory affects only new threads.

A **project** is a name for an existing folder. A thread created in a project works directly in that folder, sharing its files with the project's other threads, unless it uses a [worktree](#worktrees). Choose **New project** in the sidebar, or beside **Project** when creating a thread, and pick a folder with **Choose…** or type its path. Once a project exists, the sidebar lists it, even with no threads yet, with its own buttons to start a thread in it and to edit or remove it (also in its context menu and in **Settings**). When creating a thread, choose one under **Project**; a new thread starts in the project of the thread you were viewing. Imported sessions whose working directory is a project's folder, or one of its repository's worktrees, join that project. Removing a project asks first and removes only Mu's entry: its threads keep their folder and session, and Mu never deletes a project folder. Mu never deletes thread folders either, except a worktree you delete.

## Worktrees

When a project's folder is in a Git repository, the new-thread form also asks what to **work on**, with one search box over these choices. **Project folder** is still the default and changes nothing.

| Choice | Pick it to… | What happens |
|---|---|---|
| **New branch** | Start a task | A new worktree on a new branch, from the remote's freshly fetched default branch, or from what you enter under **Start from**: a branch, a commit, or a tag you've fetched. Type a name, or leave it empty for an automatic one such as `mu/2026-11-05-3fa9c2`. The branch has no upstream until you or the agent push it. If the fetch fails, it starts from the last fetched copy and says so. |
| **Worktrees** | Reuse a checkout | The thread works in that worktree as it is, sharing its files with anything else there. No Git changes. |
| **Pull requests** | Review or continue one | Works on the pull request's branch, exactly as under **Branches**, so you can push to it. A pull request from a fork, one whose branch is gone, or any without `gh` gets a new worktree detached at its head commit, shown by its short SHA. The list comes from `gh`. Without it, type `#123`. |
| **Branches** | Resume a branch | A local branch gets a new worktree, and fast-forwards to its upstream if it is only behind; it never moves otherwise. A branch only on a remote is fetched and tracked. A branch already checked out, which you can also type, uses that checkout. |

A sentence below the list says what creating the thread will do, with any warnings, such as unpushed commits. Problems that can be predicted, such as an invalid name or a branch that exists, say so there and block **Create thread**; others, such as a folder you can't write to, are reported when they happen. Nothing is changed until then, and Mu plans again at that moment in case something moved. While a worktree is set up its project shows it in the sidebar, and you can go on working or start other threads; creating and removing worktrees run one at a time, so they can't race for a folder or a ref, while a slow fetch or submodule holds up only its own thread. Quitting gives a checkout or removal under way up to 30 seconds to finish, as interrupting Git would leave a worktree it needs help to repair. In the sidebar, a thread in a worktree shows its branch.

**Where they go.** New worktrees go in `worktrees/{repo}/{branch}` in the working directory. To change that, set **Worktree folder** in Settings to a complete path using `{repo}` (the primary checkout's folder name), `{repoPath}` (the checkout's path), `{checkoutParent}` (its parent) and `{branch}` (with `/` and other unsafe characters replaced by `-`). A leading `~` means your home folder, and a relative path starts at the checkout, so `../{repo}.{branch}` puts them beside it. A folder that exists is never reused: the name gets `-2`, `-3` and so on. If the project's folder is a subfolder of the repository, threads work in the same subfolder of the worktree, if the branch has it.

**Ready to run.** A new worktree gets its submodules initialized, and the untracked files matched by a tracked `.worktreeinclude`, such as `.env`, are copied from the project's checkout. Copying takes regular files only, follows no symlinks, and never replaces a file. A failure in either is reported and doesn't stop the thread. A detached pull request's worktree gets neither, as what these files name would come from code under review. Git's own hooks and filters run as they do for any checkout.

**Deleting.** **Delete worktree…** is in a thread's menus and in **Worktrees…** in a project's menu, which lists its checkouts, including those made outside Mu, and can start a thread in one. It asks first, then:

1. Refuses, with nothing changed, if the worktree has uncommitted changes or untracked files, files marked assume-unchanged or skip-worktree (outside a sparse checkout), a lock, commits added on a detached HEAD that no branch or tag has, or a submodule with changes or commits of its own: on a local branch, stashed, under a tag its remote doesn't have, or at a HEAD that neither its remote nor a published commit of the repository has. That includes submodules deinitialized since, whose repositories go too, and a submodule that has worktrees of its own, which depend on its repository. If those commits aren't yours, or Mu can't tell, `git worktree remove --force` is always there. Ignored files, such as build output, are deleted with it. A thread that is running also stops it.
2. Closes the sessions of the threads in it, and waits for that. An agent that refuses and keeps running stops the deletion, now and on later tries, even after its thread is removed from Mu, until it closes the session or stops. As when archiving, one that doesn't answer within ten seconds is terminated, which disconnects its other sessions too. Meanwhile nothing new can start using the folder.
3. Runs `git worktree remove`, and archives those threads, whose sessions stay with the agent. Their folder is gone, so restoring one works only as far as the agent copes with that, and an error says so. Other worktrees and threads carry on.

Its branch stays, unless you also choose to delete it, and even then `git branch -d` keeps a branch that isn't fully merged. Mu can't delete the primary checkout or a folder a project uses. The only time Mu passes `--force` is a repository with submodules, where Git refuses every worktree without it: the checks above have passed by then, and a lock still stops it. Mu never pushes.

Mu runs Git with your configuration and credentials. A remote that asks for a password in a terminal simply fails. Fetches request a single ref, with no tags, pruning or submodules, and the only refs Mu overwrites are remote-tracking ones. Mu keeps no refs of its own. A pull request is fetched from the remote of the repository `gh` names, on the same host if one is, and the sentence says which. Tracking a branch that a single-branch clone doesn't fetch adds it to what that remote fetches.

## Agent connections

Opening a thread connects it: a new thread starts its agent and creates a session on the agent, without sending a prompt, so its options are ready before the first message. A thread you never send anything in still leaves that empty session with the agent, for you to clean up. If you send while the agent is starting, the message goes out as soon as it is connected: the draft stays in the composer, read-only, until then. **Cancel sending** or Escape takes it back, and if connecting fails it stays there unsent. The first message names the thread, replacing any placeholder title from the agent, and tells agents that store titles once its turn ends, unless the agent chose a title of its own by then. Opening a saved session starts its agent and loads history automatically, including the selected thread at application startup.

Threads that use the same agent configuration share one agent process, as ACP allows: each thread is a separate session on that connection, and sessions can work at the same time. Discovery uses the same process. The process starts in the working directory. A saved agent's process keeps running while idle, so opening, archiving, and discovering do not restart it; it stops when Mu quits or when that saved agent is edited or removed and no session still uses it. Threads using a one-off command or an older command snapshot stop their process when their last session closes. If it exits, its threads reconnect on a new process when next used. Threads with different configurations use different processes.

Each sidebar row shows the thread's agent, its folder when it is not a project or Mu-created folder, and last activity, or its most urgent state (working, needs permission, agent error). The sidebar toggle preserves its collapsed state across restarts; New thread, Import sessions, and Settings remain available in the collapsed rail.

Opened from the Dock or Finder on macOS, Mu reads `PATH` from your login shell, so it finds agents the way a terminal would. Only `PATH` is read: other variables your shell exports, such as API keys, reach agents only if you start Mu from a terminal.

Agent commands are executable names plus arguments. Quotes preserve spaces; there is **no shell expansion**, redirection, or automatic `~`/`$VARIABLE` expansion. Use absolute paths, a wrapper executable, or `env NAME=value agent …` when needed. Do not put secrets in the command: the command is saved with the thread.

Optional startup flags prefill the form or isolate Mu's data:

```sh
./bin/mu -agent 'my-agent --acp'
./bin/mu -data-dir /absolute/path/to/isolated-history
```

**Enter** sends a message; **Shift+Enter** inserts a newline. You can also click **Send**. **Stop** requests ACP cancellation and immediately shows the turn's unfinished tool calls as cancelled. If the agent has not ended the turn ten seconds later, giving it time to finish a tool call or clean up, Mu abandons the turn and closes that session, which ACP requires the agent to treat as a cancellation; other sessions on the process are unaffected. Mu terminates the process only if the agent stops reading Mu's messages or does not answer that close within ten seconds. An error response to a prompt is shown in the thread and does not close the session. A turn that ends because the agent refused the prompt or reached a token or request limit says so. Stop an active turn before archiving, removing, or forking it. Drafts survive switching threads within the app, but are not saved across app restarts.

## Session options and updates

The composer toolbar shows every option the agent advertises, such as model and reasoning level, as a compact menu, so the current values are always visible; a tooltip carries the option's description. Select options keep their groups, boolean options offer On and Off, and legacy modes appear only when configuration options are absent. Successful changes replace the full advertised option list, so dependent controls stay in sync. Rejected changes leave the acknowledged values intact and show the agent's error.

Below the composer, Mu shows the branch of the thread's folder, the short commit when HEAD is detached, and the folder's name when it is a linked worktree. It reads these with local `git` commands while the thread is in view, every few seconds and after each turn. Outside a repository, or without `git`, the line is absent.

If the `gh` command is available and signed in, the same line shows the number and state of the branch's pull request, such as **PR #12 · Open**, which you can click to open in your browser. Mu runs `gh pr view` for the thread in view: when you open it, after each turn, when its branch changes, and once a minute. With no `gh`, no sign-in, no GitHub remote or no pull request, it shows nothing.

Tool calls that report ACP diffs show their size on the row, such as **+3 −1**, and the changes when expanded, for any agent. Permission cards show them too. Very large changes show their first lines and say how many more there are.

The composer keeps a **context-usage indicator** visible, with the reported token count, window limit, percentage, and a bar that turns amber at 70% and red at 90%. Reported **session cost** appears alongside it in the agent's currency. Click the indicator for last-turn input/output, thinking and cache counts, and the stop reason; the same panel shows when the agent last updated the session and its metadata. Missing usage is explicitly shown as not reported, never zero. Provider-specific limits and other `_meta` data remain inspectable under **Agent metadata** there, without guessing their meaning.

Type **/** at the start of the draft for a scrollable command picker above the composer. It filters the agent's advertised commands as you type. **Up/Down** selects, **Enter/Tab** inserts, and **Escape** dismisses. Clicking a command also inserts it and returns focus to the draft. Completion never sends a prompt; add arguments and send separately. If the thread could not connect when it opened, typing **/** tries again, without sending a prompt.

Select text in an agent's reply and an **Add to prompt** popup appears near the pointer. Clicking it adds the selection to the draft as a Markdown quote on a new line, keeps what you've already written, and returns focus to the draft. The quote is the text as it reads on screen, so formatting such as bold or list markers isn't included. The popup closes with Escape, or when the selection ends or focus leaves the reply.

Long model/option lists scroll within a bounded menu rather than extending past the window. Keyboard selection scrolls the highlighted choice into view. Repeated display prefixes, such as “Thinking: high” underneath a Thinking label, are shortened to “high”; protocol values are unchanged.

Drag the sidebar's edge to resize it, up to half the window; its width is saved. Dragging it narrow past its minimum snaps it to the collapsed rail with a spring, and dragging the rail's edge brings it back. The collapse button animates over 220 ms, highlights fade over 120 ms, and menus/dialogs/screens enter and leave over 160 ms. Disclosures expand smoothly, context bars ease toward new reported usage, and an activity spinner identifies an outstanding request. While an agent works, its status text in the sidebar and composer shimmers. Streamed text is not delayed for a typewriter effect. Motion respects the desktop's reduced-motion preference.

Session updates are processed while idle as well as during prompts. Mu handles user/assistant/thought chunks, grouped into messages by the agent's message IDs when it sends them (otherwise adjacent chunks of the same kind form one message); tool calls and their partial updates, which change only the fields they include; legacy and ID-addressed plans/removals, available commands, current modes, configuration changes, session info, and usage updates exposed by the ACP SDK. Non-text content is retained with a readable placeholder and inspectable protocol details. Setup notifications are buffered; a successful `session/load` replaces the in-memory conversation with the replay, and session options or modes in the load response take precedence over older values replayed before it. A failed load leaves the old view unchanged and displays an error, rather than exposing a partial replay as complete history. Unsupported experimental capabilities, such as compaction, are not advertised.

## Permission requests

Agents ask before some tool calls with ACP `session/request_permission`. Mu shows each request as a card, in the order it receives them, until you choose one of the agent's options; the sidebar marks the thread **Needs permission**. A request often names only a tool call the agent already reported, so the card describes that tool call: its title, kind (such as Execute or Edit), locations, and content such as diffs, or else its input, with the full protocol details on request. Fields in the request take precedence. Options that remember a choice ("always") are remembered by the agent, not by Mu; Mu has no automatic approvals.

Requests can arrive outside a prompt turn and are shown the same way. Mu answers a request as cancelled only when you stop its session's turn (as ACP requires), or when the session is archived, removed, reloaded, or disconnected, or Mu quits. An agent cannot yet withdraw a pending request with `$/cancel_request`: the ACP SDK Mu uses does not support it, so such a card stays until you answer it.

## Pi

Mu includes an ACP adapter for [Pi](https://pi.dev), so nothing else is needed but Pi itself, version 1.5.0 or newer. Mu looks for Pi in the background when it starts and when you open Settings, New thread or Import sessions. Once found, Pi is an agent you can choose, and the default if nothing else is. If it's somewhere other than your `PATH`, enter its path under **Built-in** in Settings. The adapter runs inside Mu and drives `pi --mode rpc`, with one Pi process per open session; each takes roughly 50 to 100 MB.

Mu doesn't install Pi or sign in for you. Sign in with Pi itself: run `pi` in a terminal, then `/login`. Without a provider, a thread opens and its history loads, but offers no model and can't send.

- **Project trust.** Pi's RPC mode can't show its trust prompt, and Mu doesn't approve anything on your behalf. In a folder you haven't trusted, Pi silently skips the folder's `.pi` settings, prompts, skills and extensions, so they won't be available in Mu. `AGENTS.md` and `CLAUDE.md` still load. To use them, run `/trust` in Pi in that folder, or set `defaultProjectTrust` in Pi's settings.
- **File changes.** Pi's edit and write tools report ACP diffs, which appear as a collapsible diff on the tool call. A write doesn't say what it replaced, so all of its text reads as added.
- **Tool use.** Pi doesn't ask before every tool call, so expect few permission requests. They come only from Pi extensions: a `confirm` or `select` becomes a permission card. An extension's request for text has no equivalent in Mu and is declined.
- **Slash commands.** The picker shows Pi's prompt templates, skills and extension commands, plus `/compact`. Pi's other terminal commands, such as `/model` and `/tree`, don't work over RPC: typed anyway, they'd reach the model as plain text.
- **Sessions** stay Pi's, in its usual session files, so Import lists them and Pi can open sessions made in Mu. A session gets its file with its first turn. Until then Mu keeps a small marker in its data directory, so a thread you never sent anything in still reopens, while a session deleted from Pi is reported rather than silently replaced.
- **Deleted worktrees.** Pi can't start in a folder that's gone, so a thread whose worktree was deleted says so instead of reconnecting. Its conversation stays in Pi's session files.
- **Using one session in both Mu and Pi at once** isn't coordinated. Neither corrupts it, but each adds its own branch, and reopening shows the one written last. The other stays in Pi's `/tree`.

## Discovery and thread semantics

Choose **Import sessions** in the sidebar, select a saved agent, then **Find sessions**. Discovery follows the agent's pagination across all exposed directories on the agent's shared process, starting it in the working directory if needed, without creating/loading sessions or sending prompts. Filter by title, directory, or session ID, select rows, and choose **Add selected to Mu**. Nothing is selected or imported automatically. History loads only when an imported thread is opened.

- Session identity is scoped to the saved configuration ID and its command snapshot, not just the session ID or agent display name. Editing a command cannot silently retarget existing references. Mu cannot infer that two different configurations share a session store, or detect an external account/storage change hidden behind an unchanged command.
- Refreshing discovery updates cached titles/activity for managed references. A reference missing from a complete listing is marked **not listed**, not deleted. Failed or incomplete listings do not reconcile references.
- A session's working directory cannot change in ACP. If a listing reports a different one, Mu shows a notice and suggests re-importing. Additional directories are supplied on each load: if a listing reports different ones, Mu keeps loading the session with the directories accepted at import and shows a notice. Listings never change the directories Mu supplies.
- **Fork** and **Archive** are buttons in the thread header. **Reload history** (or **Retry connection**) and **Remove from Mu…** are in its **More thread actions** menu; Archive and Remove are also in a thread row's context menu.
- A load error keeps the existing reference and shows **Retry connection** beside the error. Mu never creates a replacement session automatically. **Reload history** closes the connection and replaces the view with a fresh replay, including external changes.
- **Archive** hides a thread from the active list and closes its idle agent session. **Archived → Restore** loads it again.
- To act on several threads, choose **Select** above the list, or Ctrl/Cmd-click rows, and Shift-click for a range. The bar above **Import sessions** archives (or, in the archived list, restores) or removes them; a row's context menu does the same for a selection of several. Busy threads are left unchanged and stay selected. Escape or **Done** ends the selection.
- **Rename…** is in a thread row's context menu and the **More thread actions** menu; double-clicking the header's title also opens it. A title you choose stays: agent updates and discovery no longer replace it. If the thread's agent is running and advertises `sessionCapabilities.title`, Mu also stores the title with `session/set_title`, which ACP still marks unstable; otherwise the title is kept in Mu only.
- **Remove from Mu** requires confirmation and removes only Mu's reference. It does not call agent-side deletion or remove workspace files. The session can be imported again.
- **Fork** sends ACP `session/fork` on the source thread's connection, connecting it first if needed; the agent creates the fork. The source stays connected and unchanged. The fork response contains no history, so Mu closes the fork, saves the new reference, and loads its history from the agent on the same connection when it is opened. No transcript is copied or supplied as a prompt; workspace files are not rolled back.
- Archiving, removing, reloading, and shutdown request `session/close` for a live session; the process stops only as described under Agent connections. A close failure is surfaced; an agent that no longer has the session (ACP error -32002) counts as closed. If the agent does not answer within ten seconds, Mu terminates the process so cleanup cannot hang, which also disconnects other sessions on it. When Mu stops a process, it first closes the agent's input and waits two seconds, then requests termination (SIGTERM on Unix) and waits one more second before killing it, so the agent can save its sessions. Starting an agent may take up to 30 seconds and loading or creating a session up to two minutes; a load Mu gives up on is closed in case the agent completes it. Mu keeps waiting for a session or fork it stopped waiting for, and closes it if the agent creates it later; a failure to close it is reported with the session ID. Stopping a new thread while its session is being created keeps that session.
- Another client can still use an imported session. Mu prevents conflicting local operations on the same managed reference, but does not promise cross-client locking.

## Local data and trust

Session references, cached titles/activity, workspace roots, saved agents, projects, the working directory, and sidebar/archive preferences live in `$XDG_DATA_HOME/mu/threads.json`, or `~/.local/share/mu/threads.json`. The unversioned local data contains no messages, thinking, tools, usage, session options, raw metadata, or running-turn state. Those exist only in memory and are reconstructed from the agent.

Writes use a private temporary file and atomic replacement; an exclusive lock prevents two instances from overwriting the same registry. Metadata changes are saved periodically and at lifecycle boundaries. Invalid data causes startup to fail without overwriting it. Use `-data-dir` for an isolated test instance.


**Mu is not a sandbox.** Agent executables inherit your environment and run with your user permissions. Only run agents and commands you trust. Permission controls answer requests the agent actually sends; Mu cannot force every agent tool to request approval. Mu performs no file access or command execution on an agent's behalf. On Unix, cleanup terminates the agent's process group; on Windows only direct child termination is implemented.

Mu sends no telemetry. Its only network activity of its own is `gh pr view` above, and for a project in a repository, `git ls-remote` and `gh pr list` when you open the new-thread form and `git fetch` when you start a thread that needs it: all made by your own `git` and `gh`, with your own credentials. Agent processes may use their own network services. Saved commands, titles, and workspace paths can contain sensitive data; the local registry is not encrypted. Agent-owned histories follow the agent’s own storage policy.

## Client capabilities

Mu advertises no client file system (`fs/*`) or terminal (`terminal/*`) methods: agents read and write files and run commands themselves. ACP v2 removes both from the protocol. If an agent requests them anyway, Mu answers that the method is not found. A tool call that refers to a terminal is shown as a placeholder.

## Required agent capabilities

Mu requires `loadSession: true` and `sessionCapabilities.list`, `.close`, and `.fork` in the initialization response, in addition to the baseline ACP methods. An agent missing any required capability is rejected with an error naming the missing methods. Mu does not compensate by saving transcripts, injecting context, or creating replacement sessions. Native forking uses the SDK’s currently unstable `session/fork` API and is part of this required contract. Agent-side deletion is not a Mu feature.

Mu currently speaks ACP v1. It restores sessions with `session/load`, which replays history; it does not use `session/resume`, which restores without replay, so it does not require it. ACP v2 is not yet supported.
