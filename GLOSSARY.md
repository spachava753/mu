# Mu

Mu lets users work with agent-owned conversations and choose which sessions appear in their workspace.

## Language

**Agent session**:
An ongoing conversation whose history and continuation state belong to the agent.
_Avoid_: Local conversation, Mu-owned history

**Thread**:
A session reference managed by Mu, with local presentation and archive preferences. A new thread can exist before the agent creates its session.
_Avoid_: Session copy, transcript backup

**Working directory**:
The general folder in which each thread without a project gets its own new, randomly named folder.
_Avoid_: Project folder, launch directory

**Project**:
A named, existing folder that its threads work in, unless they use a worktree of its repository. Removing a project leaves its threads and folder unchanged.
_Avoid_: Workspace, repository

**Worktree**:
A checkout of a project's repository, in the sense of `git worktree`, that a thread can work in to have its own files and branch. Git owns it: one made outside Mu is the same as one made in Mu. Deleting one archives its threads and leaves the agent's sessions alone.
_Avoid_: Environment, sandbox

**Attachment**:
A file sent with a prompt, after its text. Whether it can be used is for the agent to say.
_Avoid_: Upload

**Agent connection**:
One running agent process that serves the sessions of every thread using the same agent configuration, and its discovery. A saved agent's connection stays open while idle.
_Avoid_: Thread process

**Built-in agent**:
An agent whose ACP adapter ships with Mu and runs inside it, named by a command such as `mu:pi`. The agent itself is installed and signed in separately.
_Avoid_: Bundled agent

**Discovery**:
Finding the sessions an agent makes available, without adding them to Mu's managed threads.
_Avoid_: Automatic import, history synchronization

**Import**:
Explicitly adding references to selected discovered sessions. Import does not copy or transfer ownership of their conversations.
_Avoid_: Download history, copy session

**Replay**:
The agent's account of an existing conversation, used to reconstruct its visible history.
_Avoid_: Client-owned history

**Remove from Mu**:
Removing a managed session reference while leaving the agent's session and workspace files unchanged.
_Avoid_: Delete session

**Fork**:
An independent agent session branched from another session by the agent itself.
_Avoid_: Transcript-copy fork
