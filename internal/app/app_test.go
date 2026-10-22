package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/testutil"
)

func openTest(t *testing.T, dir string) *App {
	t.Helper()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}
func findThread(t *testing.T, a *App, id string) ThreadView {
	t.Helper()
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
	id, err := a.Create(command, t.TempDir())
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
	for _, capability := range []string{"load", "list", "resume", "close", "fork"} {
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
	if _, err := a.Create("agent", t.TempDir()); err == nil {
		t.Fatal("expected save failure")
	}
	if len(a.Snapshot().Threads) != 0 || a.Snapshot().StorageError == "" {
		t.Fatal("phantom thread or silent save error")
	}
	if err := os.Remove(filepath.Join(dir, "threads.json")); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReductionAndTerminalExtensions(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, "agent")
	a.mu.Lock()
	r := a.runtimeLocked(id)
	r.session = "s"
	r.acceptUpdates = true
	a.mu.Unlock()
	for _, text := range []string{"hello ", "world"} {
		a.update(id, r, acp.SessionNotification{SessionId: "s", Update: acp.UpdateAgentMessageText(text)})
	}
	if got := findThread(t, a, id).Entries; len(got) != 1 || got[0].Text != "hello world" {
		t.Fatal(got)
	}
	a.update(id, r, acp.SessionNotification{SessionId: "wrong", Update: acp.UpdateAgentMessageText("ignore")})
	a.terminalOutput(id, r, 0, "s", "term", "early output")
	a.update(id, r, acp.SessionNotification{SessionId: "s", Update: acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{ToolCallId: "tool", Title: "Run", Content: []acp.ToolCallContent{acp.ToolTerminalRef("term")}}}})
	if lastText(findThread(t, a, id)) != "early output" {
		t.Fatal("lost output before tool notification")
	}
	a.terminalOutput(id, r, 0, "s", "term", "completed output")
	if lastText(findThread(t, a, id)) != "completed output" {
		t.Fatal("terminal did not update")
	}
	a.update(id, r, acp.SessionNotification{SessionId: "s", Update: acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{ToolCallId: "tool", Meta: map[string]any{"terminal_output": map[string]any{"terminal_id": "term", "data": " delta"}}}}})
	if lastText(findThread(t, a, id)) != "completed output delta" {
		t.Fatal("lost Pi terminal delta")
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
