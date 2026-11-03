package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/testutil"
)

func openTest(t *testing.T, dir string) *App {
	t.Helper()
	a, err := Open(dir, filepath.Join(dir, "work"))
	if err != nil {
		t.Fatal(err)
	}
	// Keep tests of unresponsive agents fast. TestStopWaitsForSlowCancellation
	// covers the real deadline.
	a.agentDeadline = 2 * time.Second
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}
func findThread(t *testing.T, a *App, id string) ThreadView {
	t.Helper()
	// Every session change must invalidate the snapshot's cached deep copy.
	a.mu.Lock()
	for _, r := range a.runtimes {
		if r.cloneVersion == r.sessionVersion {
			cached, _ := json.Marshal(r.sessionClone)
			live, _ := json.Marshal(r.Session.Clone())
			if !bytes.Equal(cached, live) {
				a.mu.Unlock()
				t.Fatalf("snapshot session is stale:\ncached %s\nlive   %s", cached, live)
			}
		}
	}
	a.mu.Unlock()
	for _, v := range a.Snapshot().Threads {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("thread %s not found", id)
	return ThreadView{}
}
func await(t *testing.T, a *App, id string, condition func(ThreadView) bool) ThreadView {
	t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		v := findThread(t, a, id)
		if condition(v) {
			return v
		}
		select {
		case <-a.Changes():
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timeout: %+v", v)
		}
	}
}
func sendDone(t *testing.T, a *App, id, text string) ThreadView {
	t.Helper()
	if err := a.Send(id, text); err != nil {
		t.Fatal(err)
	}
	return await(t, a, id, func(v ThreadView) bool { return !v.Running && !v.Configuring })
}
func createTest(t *testing.T, a *App, command string) string {
	t.Helper()
	id, err := a.Create(command, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func lastText(v ThreadView) string {
	if len(v.Entries) == 0 {
		return ""
	}
	return v.Entries[len(v.Entries)-1].Text
}

func TestStdioLifecycle(t *testing.T) {
	command := testutil.Agent(t)
	dir := t.TempDir()
	a := openTest(t, dir)
	id := createTest(t, a, command)
	v := sendDone(t, a, id, "hello")
	if v.Error != "" || lastText(v) != "Echo: hello" || len(v.Entries) != 2 {
		t.Fatalf("unexpected turn: %+v", v)
	}
	session := v.SessionID
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	v = sendDone(t, a, id, "again")
	if v.SessionID != session || len(v.Entries) != 4 || lastText(v) != "Echo: again" {
		t.Fatalf("load duplicated or lost history: %+v", v)
	}
	fork, err := a.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	v = sendDone(t, a, fork, "branch")
	if v.SessionID == session || len(v.Entries) != 6 || v.Entries[1].Text != "Echo: hello" || lastText(v) != "Echo: branch" {
		t.Fatalf("fork lost context: %+v", v)
	}
	if len(findThread(t, a, id).Entries) != 4 {
		t.Fatal("fork changed original")
	}
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(id, "blocked"); err == nil {
		t.Fatal("sent to archived thread")
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if err := a.Archive(id, false); err != nil {
		t.Fatal(err)
	}
	if v := sendDone(t, a, id, "restored"); lastText(v) != "Echo: restored" {
		t.Fatal(v)
	}
	if err := a.Delete(fork); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	if len(a.Snapshot().Threads) != 1 {
		t.Fatal("delete did not persist")
	}
}

func TestMissingSessionCapabilitiesRejectAgent(t *testing.T) {
	command := testutil.Agent(t)
	for _, capability := range []string{"load", "list", "close", "fork"} {
		t.Run(capability, func(t *testing.T) {
			a := openTest(t, t.TempDir())
			before := len(fixtureCalls(t, command))
			id := createTest(t, a, command+" no-"+capability)
			v := sendDone(t, a, id, "must not reach agent")
			if !strings.Contains(v.Error, "session/"+capability) || !strings.Contains(v.Error, "cannot use") || v.SessionID != "" {
				t.Fatalf("unsupported agent accepted: %+v", v)
			}
			for _, call := range fixtureCalls(t, command)[before:] {
				if call != "initialize" {
					t.Fatal("rejected agent received session request", call)
				}
			}
		})
	}
}

func TestResumeIsNotRequired(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" no-resume")
	if v := sendDone(t, a, id, "hello"); v.Error != "" || lastText(v) != "Echo: hello" {
		t.Fatalf("agent without session/resume rejected: %+v", v)
	}
}

func TestPermissionsAndStop(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if err := a.Send(id, "permission"); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) == 1 })
	a.Answer(id, v.Permissions[0].ID, "not-an-option")
	if !findThread(t, a, id).Running {
		t.Fatal("invalid choice answered permission")
	}
	a.Answer(id, v.Permissions[0].ID, "no")
	v = await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if !strings.Contains(lastText(v), `"optionId":"no"`) {
		t.Fatal(v)
	}
	for _, prompt := range []string{"wait", "hang", "permission"} {
		t.Run(prompt, func(t *testing.T) {
			if err := a.Send(id, prompt); err != nil {
				t.Fatal(err)
			}
			await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) > 0 || strings.HasSuffix(lastText(v), "Waiting") })
			if err := a.Send(id, "duplicate"); err == nil {
				t.Fatal("two turns in one session")
			}
			if _, err := a.Fork(id); err == nil {
				t.Fatal("forked running thread")
			}
			if err := a.Delete(id); err == nil {
				t.Fatal("deleted running thread")
			}
			a.Stop(id)
			v := await(t, a, id, func(v ThreadView) bool { return !v.Running })
			if !strings.HasPrefix(lastText(v), "Stopped.") {
				t.Fatal(v)
			}
		})
	}
}

func TestStopWaitsForSlowCancellation(t *testing.T) {
	a := openTest(t, t.TempDir())
	a.agentDeadline = AgentDeadline
	id := createTest(t, a, testutil.Agent(t)+" slow-cancel")
	if err := a.Send(id, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	started := time.Now()
	a.Stop(id)
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if time.Since(started) < 3*time.Second || v.Error != "" || !v.Connected || !strings.HasPrefix(lastText(v), "Stopped.") {
		t.Fatalf("agent was terminated instead of finishing its cancellation: %+v", v)
	}
}

func TestFailureAndEmptyTurn(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	v := sendDone(t, a, id, "empty")
	if !strings.Contains(lastText(v), "without a response") {
		t.Fatal(v)
	}
	v = sendDone(t, a, id, "exit")
	if v.Error == "" || !strings.Contains(lastText(v), "Agent error:") {
		t.Fatal("failure not visible and persisted", v)
	}
	v = sendDone(t, a, id, "recover")
	if v.Error != "" || lastText(v) != "Echo: recover" {
		t.Fatal("could not reconnect", v)
	}
	bad := createTest(t, a, "/no/such/agent")
	if v := sendDone(t, a, bad, "hello"); v.Error == "" {
		t.Fatal("missing executable not reported")
	}
}

func TestCloseCancelsRunningTurnAndReleasesLock(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	id := createTest(t, a, testutil.Agent(t))
	if err := a.Send(id, "hang"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
	b := openTest(t, dir)
	if findThread(t, b, id).Running {
		t.Fatal("turn still running after close")
	}
}

func TestAtomicFailureDoesNotCreatePhantomThread(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	// A directory at the destination makes atomic replacement fail even as root.
	if err := os.Mkdir(filepath.Join(dir, "threads.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create("agent", ""); err == nil {
		t.Fatal("expected save failure")
	}
	if len(a.Snapshot().Threads) != 0 || a.Snapshot().StorageError == "" {
		t.Fatal("phantom thread or silent save error")
	}
	if err := os.Remove(filepath.Join(dir, "threads.json")); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReductionAndToolExtensions(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, "agent")
	a.mu.Lock()
	r := a.runtimeLocked(id)
	r.session = "s"
	r.acceptUpdates = true
	a.mu.Unlock()
	for _, text := range []string{"hello ", "world"} {
		a.update(id, r, acp.SessionNotification{SessionID: "s", Update: acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(text))})
	}
	if got := findThread(t, a, id).Entries; len(got) != 1 || got[0].Text != "hello world" {
		t.Fatal(got)
	}
	a.update(id, r, acp.SessionNotification{SessionID: "wrong", Update: acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock("ignore"))})
	// Mu runs no terminals: a terminal reference is shown as a placeholder.
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: toolCallUpdate("tool", "Run", acp.TerminalToolCallContent("term"))})
	if got := lastText(findThread(t, a, id)); got != "Terminal: term\n" {
		t.Fatalf("terminal reference: %q", got)
	}
	// Extension metadata is retained for inspection but is not a terminal transport.
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: toolPatchUpdate("tool", func(u *acp.SessionUpdate) {
		u.Meta = acp.Meta{"terminal_output": map[string]any{"terminal_id": "term", "data": " delta"}}
	})})
	if v := findThread(t, a, id); lastText(v) != "Terminal: term\n" || !strings.Contains(v.Entries[len(v.Entries)-1].Details, "terminal_output") {
		t.Fatal("metadata changed terminal output or was dropped", v.Entries)
	}
}

func TestConversationIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	id := createTest(t, a, testutil.Agent(t))
	sendDone(t, a, id, "Public title")
	sendDone(t, a, id, "private-conversation-marker")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"entries"`, `"running"`, `"session"`, `"rawOutput"`, `"previous_conversation"`, "private-conversation-marker"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("persisted conversation field %s", forbidden)
		}
	}
	a = openTest(t, dir)
	v := findThread(t, a, id)
	if v.Running || len(v.Entries) != 0 || v.Session.Usage != nil {
		t.Fatal("restored local conversation", v)
	}
	v = connected(t, a, id)
	if len(v.Entries) != 4 || lastText(v) != "Echo: private-conversation-marker" {
		t.Fatal("agent replay missing", v)
	}
}
