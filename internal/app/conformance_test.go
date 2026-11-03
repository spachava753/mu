package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
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
	if v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring }); v.Session.Modes.CurrentModeId != "ask" {
		t.Fatalf("requested mode overwrote the agent's: %s", v.Session.Modes.CurrentModeId)
	}
}

// Only persisted changes mark the registry for saving.
func TestStreamingDoesNotDirtyRegistry(t *testing.T) {
	a, id, r := protocolRuntime(t)
	a.mu.Lock()
	a.dirty = false
	a.mu.Unlock()
	a.update(id, r, acp.SessionNotification{SessionId: "s", Update: acp.UpdateAgentMessageText("streamed")})
	a.mu.Lock()
	dirty := a.dirty
	a.mu.Unlock()
	if dirty {
		t.Fatal("a message chunk marked the registry dirty")
	}
	title := "Renamed"
	a.update(id, r, acp.SessionNotification{SessionId: "s", Update: acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Title: &title}}})
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
	a.update(id, r, acp.SessionNotification{SessionId: "s", Meta: map[string]any{"trace": 1}, Update: acp.SessionUpdate{
		AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("x"), Meta: map[string]any{"chunk": true}},
	}})
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
		a.update(id, r, acp.SessionNotification{SessionId: "s", Update: acp.SessionUpdate{
			UserMessageChunk: &acp.SessionUpdateUserMessageChunk{MessageId: &messageID, Content: acp.TextBlock(text)},
		}})
	}
	chunk("ctx", "Context: repository rules")
	chunk("u1", "hello")
	got := findThread(t, a, id).Entries
	if len(got) != 2 || got[0].Text != "hello" || got[0].MessageID != "u1" || got[1].Text != "Context: repository rules" {
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
