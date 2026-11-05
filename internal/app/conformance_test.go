package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

// An error response to session/prompt does not end the session: Mu neither
// closes nor reloads it, and keeps the error visible.
func TestPromptErrorKeepsSession(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	connected(t, a, id)
	before := len(fixtureCalls(t, command))
	v := sendDone(t, a, id, "prompt-error")
	if !strings.Contains(v.Error, "provider overloaded") || !v.Connected {
		t.Fatalf("prompt error detached the session: %+v", v)
	}
	v = sendDone(t, a, id, "again")
	if lastText(v) != "Echo: again" || !slices.ContainsFunc(v.Entries, func(e state.Entry) bool { return strings.HasPrefix(e.Text, "Agent error:") }) {
		t.Fatalf("session was not reused or the error entry was replaced: %+v", v.Entries)
	}
	calls := fixtureCalls(t, command)[before:]
	if count(calls, "session/close") != 0 || count(calls, "session/load") != 0 || count(calls, "initialize") != 0 {
		t.Fatalf("prompt error caused session churn: %v", calls)
	}
}

// A turn that ignores session/cancel is abandoned and its session closed;
// the shared process and its other sessions are unaffected.
func TestStopDeadlineClosesOnlyItsSession(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	hung, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	connected(t, a, other)
	before := len(fixtureCalls(t, command))
	if err := a.Send(hung, "hang"); err != nil {
		t.Fatal(err)
	}
	await(t, a, hung, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	if err := a.Send(other, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, other, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	a.Stop(hung)
	v := await(t, a, hung, func(v ThreadView) bool { return !v.Running && !v.Configuring })
	if !strings.HasPrefix(lastText(v), "Stopped.") || v.Connected {
		t.Fatalf("hung turn was not abandoned and detached: %+v", v)
	}
	if o := findThread(t, a, other); !o.Running || !o.Connected {
		t.Fatalf("stopping one session ended another's turn: %+v", o)
	}
	a.Stop(other)
	if o := await(t, a, other, func(v ThreadView) bool { return !v.Running }); !o.Connected || o.Error != "" {
		t.Fatalf("other session lost: %+v", o)
	}
	calls := fixtureCalls(t, command)[before:]
	if count(calls, "session/close") != 1 || count(calls, "initialize") != 0 {
		t.Fatalf("expected one targeted close on the same process: %v", calls)
	}
}

// ACP: clients should treat -32002 from session/close as a successful close.
func TestCloseOfMissingSessionSucceeds(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" close-missing")
	connected(t, a, id)
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	if v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring }); v.SessionError != "" {
		t.Fatalf("-32002 close reported as a failure: %s", v.SessionError)
	}
	if err := a.Close(); err != nil {
		t.Fatal("shutdown reported the close:", err)
	}
}

// A load Mu gives up on may still complete in the agent, so Mu closes it.
func TestAbandonedLoadIsClosed(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	sendDone(t, a, id, "hello")
	// The same session, now on an agent that never answers session/load.
	a.mu.Lock()
	for i := range a.data.Threads {
		if a.data.Threads[i].ID == id {
			a.data.Threads[i].Command = command + " wait-load"
		}
	}
	a.loadTimeout = 300 * time.Millisecond
	a.mu.Unlock()
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	if v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring }); v.SessionError == "" || v.Connected {
		t.Fatalf("load did not time out: %+v", v)
	}
	calls := fixtureCalls(t, command)
	load := slices.Index(calls, "session/load")
	if load < 0 || !slices.Contains(calls[load:], "session/close") {
		t.Fatalf("abandoned load was not closed: %v", calls)
	}
}

// ACP: clients should mark a stopped turn's unfinished tool calls cancelled.
func TestStopMarksUnfinishedToolCallsCancelled(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if err := a.Send(id, "tool-wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	a.Stop(id)
	status := map[string]string{}
	for _, e := range findThread(t, a, id).Entries {
		if e.Role == "tool" {
			status[e.ToolID] = e.Status
		}
	}
	if status["tool-run"] != "cancelled" || status["tool-done"] != "completed" {
		t.Fatalf("tool statuses after stop: %v", status)
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Running })
}

func TestStopReasonsAreExplained(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if v := sendDone(t, a, id, "refuse"); !strings.HasPrefix(lastText(v), "The agent refused this prompt.") {
		t.Fatalf("refusal: %q", lastText(v))
	}
	v := sendDone(t, a, id, "max-tokens")
	if !strings.Contains(lastText(v), "token limit") || v.Entries[len(v.Entries)-2].Text != "Partial" {
		t.Fatalf("max_tokens: %+v", v.Entries)
	}
}

// Undecodable notifications are reported with the next agent error.
func TestProtocolDiagnosticsAreReported(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if v := sendDone(t, a, id, "bad-update"); !strings.Contains(v.Error, "ACP diagnostics") || !strings.Contains(v.Error, "failed to handle notification") {
		t.Fatalf("diagnostics missing: %q", v.Error)
	}
}

// A current_mode_update the agent sends while a mode change is outstanding
// is authoritative; Mu does not overwrite it with the requested mode.
func TestAgentModeUpdateWinsOverRequest(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" legacy-options mode-fallback")
	connected(t, a, id)
	if err := a.SetMode(id, "code"); err != nil {
		t.Fatal(err)
	}
	if v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring }); v.Session.Modes.CurrentModeID != "ask" {
		t.Fatalf("requested mode overwrote the agent's: %s", v.Session.Modes.CurrentModeID)
	}
}

// Only persisted changes mark the registry for saving.
func TestStreamingDoesNotDirtyRegistry(t *testing.T) {
	a, id, r := protocolRuntime(t)
	a.mu.Lock()
	a.dirty = false
	a.mu.Unlock()
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock("streamed"))})
	a.mu.Lock()
	dirty := a.dirty
	a.mu.Unlock()
	if dirty {
		t.Fatal("a message chunk marked the registry dirty")
	}
	title := "Renamed"
	info := acp.SessionInfoUpdateSessionUpdate()
	info.Title = &title
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: info})
	a.mu.Lock()
	dirty = a.dirty
	a.mu.Unlock()
	if !dirty || findThread(t, a, id).Title != title {
		t.Fatal("a title change was not marked for saving")
	}
}

// Extension metadata is kept by notification and update kind.
func TestUpdateMetadataIsKept(t *testing.T) {
	a, id, r := protocolRuntime(t)
	chunk := acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock("x"))
	chunk.Meta = acp.Meta{"chunk": true}
	a.update(id, r, acp.SessionNotification{SessionID: "s", Meta: acp.Meta{"trace": 1}, Update: chunk})
	m := findThread(t, a, id).Session.Metadata
	if string(m["notification"]) != `{"trace":1}` || string(m["agent_message_chunk"]) != `{"chunk":true}` {
		t.Fatalf("metadata: %v", m)
	}
}

// An agent's first user message of a turn that does not start like the
// prompt is its own message, not the echo; the user's prompt stays visible.
func TestProtocolEchoRequiresPromptPrefix(t *testing.T) {
	a, id, r := protocolRuntime(t)
	a.mu.Lock()
	local := state.Entry{ID: state.ID(), Role: "user", Text: "hello"}
	r.Entries = append(r.Entries, local)
	r.job = &job{}
	r.echo = promptEcho{target: "hello", local: local.ID}
	a.mu.Unlock()
	t.Cleanup(func() { a.mu.Lock(); r.job = nil; a.mu.Unlock() })
	chunk := func(messageID, text string) {
		a.update(id, r, acp.SessionNotification{SessionID: "s", Update: withMessageID(acp.UserMessageChunkSessionUpdate(acp.TextContentBlock(text)), messageID)})
	}
	// A message whose first chunk is not text is not the echo either.
	link := "file:///rules.md"
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: withMessageID(acp.UserMessageChunkSessionUpdate(acp.ResourceLinkContentBlock("rules", link)), link)})
	chunk(link, "h")
	// A later chunk of the rejected message ("h") must not be taken as the echo.
	chunk("ctx", "Context: ")
	chunk("ctx", "h")
	chunk("ctx", "ow to behave")
	chunk("u1", "hello")
	got := findThread(t, a, id).Entries
	if len(got) != 3 || got[0].Text != "hello" || got[0].MessageID != "u1" || got[1].MessageID != link || got[2].Text != "Context: how to behave" {
		t.Fatalf("agent message replaced the prompt: %+v", got)
	}
}

// A replacement process for a configuration starts only after the previous
// one has exited, so the two never share the agent's session store.
func TestReplacementWaitsForOldProcessExit(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command+" slow-exit")
	connected(t, a, id)
	before := len(fixtureCalls(t, command))
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return v.Connected && !v.Configuring })
	calls := fixtureCalls(t, command)[before:]
	exited, initialized := slices.Index(calls, "exited"), slices.Index(calls, "initialize")
	if exited < 0 || initialized < 0 || exited > initialized {
		t.Fatalf("replacement overlapped the old process's shutdown: %v", calls)
	}
}

// Tool calls replayed while connecting belong to earlier turns; stopping the
// new turn does not mark them cancelled.
func TestStopIgnoresReplayedToolCalls(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if err := a.Send(id, "tool-wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	a.Stop(id)
	await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if err := a.Archive(id, false); err != nil {
		t.Fatal(err)
	}
	// Sending reconnects; the replay includes the unfinished historical call.
	if err := a.Send(id, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool {
		n := len(v.Entries)
		return v.Running && n > 1 && v.Entries[n-2].Text == "wait" && v.Entries[n-1].Text == "Waiting"
	})
	a.Stop(id)
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if !strings.HasPrefix(lastText(v), "Stopped.") || !v.Connected {
		t.Fatalf("stopped turn: %+v", v)
	}
	for _, e := range v.Entries {
		if e.ToolID == "tool-run" && e.Status != "in_progress" {
			t.Fatalf("replayed tool call shown as %q", e.Status)
		}
	}
}

// A cancelled startup retires its process too: a retry waits until the
// abandoned process has exited.
func TestRetryWaitsForCancelledStartup(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command+" slow-init slow-exit")
	before := len(fixtureCalls(t, command))
	if err := a.Send(id, "hello"); err != nil {
		t.Fatal(err)
	}
	waitCall(t, command, "initialize")
	a.Stop(id) // Still initializing.
	await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if v := sendDone(t, a, id, "again"); lastText(v) != "Echo: again" {
		t.Fatalf("retry failed: %+v", v)
	}
	calls := fixtureCalls(t, command)[before:]
	exited := slices.Index(calls, "exited")
	if count(calls, "initialize") != 2 || exited < 0 || exited > slices.Index(calls[1:], "initialize")+1 {
		t.Fatalf("retry overlapped the cancelled startup: %v", calls)
	}
}

// When a transport ends while other threads still hold the connection, the
// replacement waits for the old process to exit.
func TestReplacementWaitsForDeadTransport(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	first, second := createTest(t, a, command), createTest(t, a, command)
	connected(t, a, second) // Keeps a reference to the old connection.
	before := len(fixtureCalls(t, command))
	if v := sendDone(t, a, first, "exit-slow"); v.Error == "" {
		t.Fatal("transport end was not reported")
	}
	if v := sendDone(t, a, first, "again"); lastText(v) != "Echo: again" {
		t.Fatalf("reconnect failed: %+v", v)
	}
	calls := fixtureCalls(t, command)[before:]
	exited, initialized := slices.Index(calls, "exited"), slices.Index(calls, "initialize")
	if exited < 0 || initialized < 0 || exited > initialized {
		t.Fatalf("replacement overlapped the exiting process: %v", calls)
	}
}

// Removing a saved agent while its idle, kept startup is still initializing
// retires that startup behind the barrier too.
func TestRemovedAgentStartupBlocksReplacement(t *testing.T) {
	fixture := testutil.Agent(t)
	command := fixture + " slow-init slow-exit"
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.CreateWithAgent(agentID, "") // Keeps the command snapshot.
	if err != nil {
		t.Fatal(err)
	}
	before := len(fixtureCalls(t, fixture))
	// A waiter gives up during initialization; the saved agent keeps the startup.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := a.acquire(ctx, agentID, command); err == nil {
		t.Fatal("acquire outlived its context")
	}
	if n := connectionCount(a); n != 1 {
		t.Fatalf("kept startup was dropped: %d", n)
	}
	if err := a.RemoveAgent(agentID); err != nil {
		t.Fatal(err)
	}
	if v := sendDone(t, a, id, "hello"); lastText(v) != "Echo: hello" {
		t.Fatalf("send failed: %+v", v)
	}
	calls := fixtureCalls(t, fixture)[before:]
	exited := slices.Index(calls, "exited")
	if count(calls, "initialize") != 2 || exited < 0 || exited > slices.Index(calls[1:], "initialize")+1 {
		t.Fatalf("replacement overlapped the retired startup: %v", calls)
	}
}

// A startup retired after it succeeded, but before it was published, is
// closed even if its configuration is saved again meanwhile.
func TestRetiredStartupStaysRetired(t *testing.T) {
	fixture := testutil.Agent(t)
	command := fixture + " slow-init slow-exit"
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	before := len(fixtureCalls(t, fixture))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := a.acquire(ctx, agentID, command); err == nil {
		t.Fatal("acquire outlived its context")
	}
	// Hold the mutex until agent.Start has returned and the startup worker
	// waits to publish; edit the agent away and back in that window.
	a.mu.Lock()
	time.Sleep(time.Second)
	a.data.Agents[0].Command = fixture
	a.stopIdleLocked()
	a.data.Agents[0].Command = command
	a.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(fixtureCalls(t, fixture)[before:], "exited") {
		if time.Now().After(deadline) {
			t.Fatal("retired startup kept its process running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := connectionCount(a); n != 0 {
		t.Fatalf("retired startup was published: %d connections", n)
	}
}

// Stopping a new thread while session/new is pending does not abandon the
// session the agent creates: the thread keeps it, and the prompt is not sent.
func TestStopDuringSessionCreationKeepsSession(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command+" slow-new")
	before := len(fixtureCalls(t, command))
	if err := a.Send(id, "hello"); err != nil {
		t.Fatal(err)
	}
	waitCall(t, command, "session/new")
	a.Stop(id)
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if v.SessionID == "" || !v.Connected || !strings.HasPrefix(lastText(v), "Stopped.") {
		t.Fatalf("created session was not kept: %+v", v)
	}
	if v := sendDone(t, a, id, "again"); lastText(v) != "Echo: again" {
		t.Fatalf("kept session unusable: %+v", v)
	}
	calls := fixtureCalls(t, command)[before:]
	if count(calls, "session/new") != 1 || count(calls, "session/prompt") != 1 || count(calls, "$/cancel_request") != 0 || count(calls, "session/close") != 0 {
		t.Fatalf("session/new was abandoned or the stopped prompt sent: %v", calls)
	}
}

// A session the agent creates after Mu stopped waiting is closed, not
// orphaned: only the response names it.
func TestLateSessionCreationIsClosed(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	a.mu.Lock()
	a.loadTimeout = 200 * time.Millisecond
	a.mu.Unlock()
	id := createTest(t, a, command+" slow-new")
	before := len(fixtureCalls(t, command))
	if v := sendDone(t, a, id, "hello"); v.Error == "" || v.SessionID != "" {
		t.Fatalf("creation did not time out: %+v", v)
	}
	waitCall(t, command, "session/close")
	calls := fixtureCalls(t, command)[before:]
	if count(calls, "$/cancel_request") != 0 || slices.Index(calls, "session/close") < slices.Index(calls, "session/new") {
		t.Fatalf("late session was abandoned: %v", calls)
	}
}

// A fork the agent completes after Mu stopped waiting is closed, and the
// source stays usable.
func TestLateForkIsClosed(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	a.mu.Lock()
	a.forkTimeout = 200 * time.Millisecond
	a.mu.Unlock()
	id := createTest(t, a, command+" slow-fork")
	sendDone(t, a, id, "hello")
	before := len(fixtureCalls(t, command))
	if forkID, err := a.Fork(id); err == nil || forkID != "" {
		t.Fatalf("fork did not time out: %q %v", forkID, err)
	}
	waitCall(t, command, "session/close")
	calls := fixtureCalls(t, command)[before:]
	if count(calls, "$/cancel_request") != 0 || slices.Index(calls, "session/close") < slices.Index(calls, "session/fork") || len(a.Snapshot().Threads) != 1 {
		t.Fatalf("late fork was abandoned: %v", calls)
	}
	if v := sendDone(t, a, id, "again"); lastText(v) != "Echo: again" {
		t.Fatalf("source unusable: %+v", v)
	}
}

// If closing a session created after Mu stopped waiting fails, the failure
// and the session ID are reported, not discarded.
func TestLateSessionCloseFailureIsReported(t *testing.T) {
	for _, mode := range []string{"close-error", "wait-close"} {
		t.Run(mode, func(t *testing.T) {
			command := testutil.Agent(t)
			a := openTest(t, t.TempDir())
			a.mu.Lock()
			a.loadTimeout = 200 * time.Millisecond
			a.mu.Unlock()
			id := createTest(t, a, command+" slow-new "+mode)
			sendDone(t, a, id, "hello")
			deadline := time.Now().Add(8 * time.Second)
			for !strings.Contains(a.Snapshot().LifecycleError, "after Mu stopped waiting, and closing it failed") {
				if time.Now().After(deadline) {
					t.Fatalf("late close failure not reported: %q", a.Snapshot().LifecycleError)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := a.Close(); err == nil || !strings.Contains(err.Error(), "created session") {
				t.Fatalf("shutdown hid the late close failure: %v", err)
			}
		})
	}
}

// Shutdown joins a creation request that context cancellation cannot end,
// like a write blocked on an agent that stopped reading, by closing its process.
func TestShutdownJoinsBlockedCreation(t *testing.T) {
	a := openTest(t, t.TempDir())
	a.mu.Lock()
	a.agentDeadline = 300 * time.Millisecond
	a.mu.Unlock()
	c, err := a.acquire(context.Background(), "", testutil.Agent(t))
	if err != nil {
		t.Fatal(err)
	}
	blocked := func(context.Context) (*acp.NewSessionResponse, error) {
		for c.process.Alive() { // Ignores cancellation until the process ends.
			time.Sleep(time.Millisecond)
		}
		return nil, errors.New("connection closed")
	}
	if _, err := awaitCreation(a, c, t.TempDir(), 50*time.Millisecond, blocked, func(s *acp.NewSessionResponse) acp.SessionId { return s.SessionID }); err == nil {
		t.Fatal("blocked creation did not time out")
	}
	a.release(c) // Only the request's own reference remains.
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		c.process.Close() // Rescue the suite before failing.
		t.Fatal("shutdown did not join the blocked creation")
	}
}

// Opening an imported session waits for an outstanding close of it, such as
// the cleanup of a session created after Mu stopped waiting.
func TestLoadWaitsForLateSessionCleanup(t *testing.T) {
	command := testutil.Agent(t) + " slow-new wait-close"
	fixture := strings.Fields(command)[0]
	a := openTest(t, t.TempDir())
	a.mu.Lock()
	a.loadTimeout = 200 * time.Millisecond
	a.mu.Unlock()
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	before := len(fixtureCalls(t, fixture))
	sendDone(t, a, first, "hello") // Times out; the late session's close never answers.
	waitCall(t, fixture, "session/close")
	c := discoverDone(t, a, agentID)
	if c.Error != "" || len(c.Sessions) != 1 {
		t.Fatalf("late session not discovered: %+v", c)
	}
	ids, err := a.Import(c.Generation, []string{c.Sessions[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(ids[0]); err != nil {
		t.Fatal(err)
	}
	if v := await(t, a, ids[0], func(v ThreadView) bool { return !v.Configuring }); !v.Connected {
		t.Fatalf("imported session did not open after the cleanup: %+v", v)
	}
	// The close timed out and its process was killed; only then was the
	// session loaded, on a replacement process.
	calls := fixtureCalls(t, fixture)[before:]
	load, closing := slices.Index(calls, "session/load"), slices.Index(calls, "session/close")
	if load < 0 || !slices.Contains(calls[closing:load], "initialize") {
		t.Fatalf("load raced the outstanding close: %v", calls)
	}
	if err := a.Close(); err == nil || !strings.Contains(err.Error(), c.Sessions[0].ID) {
		t.Fatalf("late close failure not reported: %v", err)
	}
}

// If the process ends while connect waits for a close of its session, the
// session is loaded on a replacement process.
func TestLoadAfterCloseEndedProcessUsesReplacement(t *testing.T) {
	fixture := testutil.Agent(t)
	command := fixture + " slow-init"
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	session := sendDone(t, a, id, "hello").SessionID
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if err := a.Archive(id, false); err != nil {
		t.Fatal(err)
	}
	before := len(fixtureCalls(t, fixture))
	if err := a.Connect(id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(fixtureCalls(t, fixture)[before:], "initialize") {
		if time.Now().After(deadline) {
			t.Fatal("connect did not start the agent")
		}
		time.Sleep(time.Millisecond)
	}
	// While the agent initializes, a close of the session begins.
	identity := [3]string{"", command, session}
	closing := make(chan struct{})
	a.mu.Lock()
	a.closingSessions[identity] = closing
	a.mu.Unlock()
	var c *connection
	for c == nil || !c.started() {
		if time.Now().After(deadline) {
			t.Fatal("agent did not start")
		}
		time.Sleep(time.Millisecond)
		a.mu.Lock()
		c = a.connections[[2]string{"", command}]
		a.mu.Unlock()
	}
	// That close does not get an answer, so the process is ended.
	c.process.Close()
	a.mu.Lock()
	delete(a.closingSessions, identity)
	close(closing)
	a.mu.Unlock()
	if v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring }); !v.Connected || v.SessionError != "" {
		t.Fatalf("load used the ended process: %+v", v)
	}
	if calls := fixtureCalls(t, fixture)[before:]; count(calls, "initialize") != 2 || count(calls, "session/load") != 1 {
		t.Fatalf("expected one load on a replacement process: %v", calls)
	}
}

// A late cleanup leaves a close already in progress for the session to
// finish, rather than replacing its barrier.
func TestLateCleanupKeepsExistingClose(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	c, err := a.acquire(context.Background(), "", command)
	if err != nil {
		t.Fatal(err)
	}
	identity := [3]string{"", command, "late"}
	existing := make(chan struct{})
	a.mu.Lock()
	a.closingSessions[identity] = existing
	a.mu.Unlock()
	before := len(fixtureCalls(t, command))
	late := func(context.Context) (*acp.NewSessionResponse, error) {
		time.Sleep(100 * time.Millisecond)
		return &acp.NewSessionResponse{SessionID: "late"}, nil
	}
	if _, err := awaitCreation(a, c, t.TempDir(), 10*time.Millisecond, late, func(s *acp.NewSessionResponse) acp.SessionId { return s.SessionID }); err == nil {
		t.Fatal("creation did not time out")
	}
	a.release(c)
	// The cleanup releases the last reference when it finishes.
	deadline := time.Now().Add(5 * time.Second)
	for connectionCount(a) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("late cleanup did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	a.mu.Lock()
	kept := a.closingSessions[identity] == existing
	delete(a.closingSessions, identity)
	close(existing)
	a.mu.Unlock()
	if !kept || slices.Contains(fixtureCalls(t, command)[before:], "session/close") {
		t.Fatal("late cleanup replaced the outstanding close")
	}
}

// Waiting for a session's close honors an expired deadline even when no
// close is outstanding, so connect's retries stop at the load deadline.
func TestSessionCloseWaitHonorsDeadline(t *testing.T) {
	a := openTest(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.mu.Lock()
	err := a.awaitSessionClosedLocked(ctx, [3]string{"", "agent", "s"})
	a.mu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expired wait succeeded: %v", err)
	}
}

// A replacement attempt started late is bounded by the remaining load
// deadline, not a fresh startup timeout.
func TestReplacementAttemptStopsAtLoadDeadline(t *testing.T) {
	fixture := testutil.Agent(t)
	command := fixture + " slow-init"
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	session := sendDone(t, a, id, "hello").SessionID
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if err := a.Archive(id, false); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.loadTimeout = 700 * time.Millisecond
	a.mu.Unlock()
	before := len(fixtureCalls(t, fixture))
	started := time.Now()
	if err := a.Connect(id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(fixtureCalls(t, fixture)[before:], "initialize") {
		if time.Now().After(deadline) {
			t.Fatal("connect did not start the agent")
		}
		time.Sleep(time.Millisecond)
	}
	identity := [3]string{"", command, session}
	closing := make(chan struct{})
	a.mu.Lock()
	a.closingSessions[identity] = closing
	a.mu.Unlock()
	var c *connection
	for c == nil || !c.started() {
		if time.Now().After(deadline) {
			t.Fatal("agent did not start")
		}
		time.Sleep(time.Millisecond)
		a.mu.Lock()
		c = a.connections[[2]string{"", command}]
		a.mu.Unlock()
	}
	// The close ends the process; the replacement's 500ms initialization
	// cannot finish within the remaining deadline.
	c.process.Close()
	a.mu.Lock()
	delete(a.closingSessions, identity)
	close(closing)
	a.mu.Unlock()
	v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if elapsed := time.Since(started); v.Connected || v.SessionError == "" || elapsed > 950*time.Millisecond {
		t.Fatalf("replacement outlived the load deadline (%s): %+v", elapsed, v)
	}
}

// Streamed updates are applied in order, all before the prompt's response.
func TestStreamedUpdatesStayInOrder(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	var want strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&want, "%d,", i)
	}
	if got := lastText(sendDone(t, a, id, "stream")); got != want.String() {
		t.Fatalf("streamed text out of order or incomplete at the response: %.80q…", got)
	}
}

// Mu answers the agent's client file system and terminal requests as unknown
// methods; it advertises neither.
func TestClientMethodsAreUnknown(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	v := sendDone(t, a, id, "client-methods")
	for i := range 3 {
		if want := fmt.Sprintf("client method %d: -32601", i); !strings.Contains(lastText(v), want) {
			t.Fatalf("missing %q in %q", want, lastText(v))
		}
	}
}
