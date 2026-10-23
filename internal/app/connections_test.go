package app

import (
	"slices"
	"testing"

	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func count(calls []string, method string) int {
	n := 0
	for _, call := range calls {
		if call == method {
			n++
		}
	}
	return n
}

func connectionCount(a *App) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.connections)
}

func TestThreadsShareAgentConnection(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	before := len(fixtureCalls(t, command))
	// A turn waiting on one session does not block another on the same process.
	if err := a.Send(first, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, first, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	if v := sendDone(t, a, second, "hello"); lastText(v) != "Echo: hello" || len(v.Entries) != 2 {
		t.Fatalf("second session's updates were misrouted: %+v", v.Entries)
	}
	a.Stop(first)
	v := await(t, a, first, func(v ThreadView) bool { return !v.Running })
	if !slices.ContainsFunc(v.Entries, func(e state.Entry) bool { return e.Text == "Waiting" }) || slices.ContainsFunc(v.Entries, func(e state.Entry) bool { return e.Text == "Echo: hello" }) {
		t.Fatalf("first session's updates were misrouted: %+v", v.Entries)
	}
	if !findThread(t, a, second).Connected {
		t.Fatal("graceful stop disconnected another session")
	}
	calls := fixtureCalls(t, command)[before:]
	if count(calls, "initialize") != 1 || count(calls, "session/new") != 2 {
		t.Fatalf("threads of one agent did not share a process: %v", calls)
	}
	if v.Cwd == findThread(t, a, second).Cwd {
		t.Fatal("threads share a folder")
	}
	// The process ends with its last session.
	if err := a.Delete(first); err != nil {
		t.Fatal(err)
	}
	if err := a.Archive(second, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, second, func(v ThreadView) bool { return !v.Configuring })
	if n := connectionCount(a); n != 0 {
		t.Fatalf("%d connections remain after their sessions closed", n)
	}
}

func TestConnectionRestartsAfterAgentExit(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	first, second := createTest(t, a, command), createTest(t, a, command)
	connected(t, a, second)
	if v := sendDone(t, a, first, "exit"); v.Error == "" {
		t.Fatal("agent exit was not reported")
	}
	await(t, a, second, func(v ThreadView) bool { return !v.Connected })
	// Both sessions load again on one replacement process.
	if v := sendDone(t, a, second, "after exit"); v.Error != "" || lastText(v) != "Echo: after exit" {
		t.Fatalf("second thread did not reconnect: %+v", v)
	}
	if v := sendDone(t, a, first, "again"); v.Error != "" || lastText(v) != "Echo: again" {
		t.Fatalf("first thread did not reconnect: %+v", v)
	}
	if n := connectionCount(a); n != 1 {
		t.Fatalf("expected one replacement connection, got %d", n)
	}
}
