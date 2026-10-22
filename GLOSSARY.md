# Mu

Mu lets users work with agent-owned conversations and choose which sessions appear in their workspace.

## Language

**Agent session**:
An ongoing conversation whose history and continuation state belong to the agent.
_Avoid_: Local conversation, Mu-owned history

**Thread**:
A session reference managed by Mu, with local presentation and archive preferences. A new thread can exist before the agent creates its session.
_Avoid_: Session copy, transcript backup

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
