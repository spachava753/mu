package app

import (
	"slices"
	"testing"
	"time"

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
	// A saved agent's process outlives its sessions, and discovery uses it.
	if err := a.Delete(first); err != nil {
		t.Fatal(err)
	}
	if err := a.Archive(second, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, second, func(v ThreadView) bool { return !v.Configuring })
	if n := connectionCount(a); n != 1 {
		t.Fatalf("saved agent's idle process was not kept: %d connections", n)
	}
	if c := discoverDone(t, a, agentID); c.Error != "" || !c.Complete {
		t.Fatal(c)
	}
	if err := a.Archive(second, false); err != nil {
		t.Fatal(err)
	}
	connected(t, a, second)
	calls = fixtureCalls(t, command)[before:]
	if count(calls, "initialize") != 1 || count(calls, "session/list") != 1 || count(calls, "session/load") != 1 {
		t.Fatalf("discovery or reopening started another process: %v", calls)
	}
	// Removing the saved agent stops the process once its last session closes.
	if err := a.RemoveAgent(agentID); err != nil {
		t.Fatal(err)
	}
	if n := connectionCount(a); n != 1 {
		t.Fatal("process in use was stopped")
	}
	if err := a.Archive(second, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, second, func(v ThreadView) bool { return !v.Configuring })
	if n := connectionCount(a); n != 0 {
		t.Fatalf("%d connections remain for a removed agent", n)
	}
}

func TestEditingSavedAgentStopsIdleProcess(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	if c := discoverDone(t, a, agentID); c.Error != "" {
		t.Fatal(c)
	}
	if n := connectionCount(a); n != 1 {
		t.Fatalf("discovery did not keep the saved agent's process: %d", n)
	}
	if _, err := a.SaveAgent(state.AgentConfig{ID: agentID, Name: "Fixture", Command: command + " session-ui"}, true); err != nil {
		t.Fatal(err)
	}
	if n := connectionCount(a); n != 0 {
		t.Fatalf("idle process for the old command kept running: %d", n)
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

// A discovery that times out on a responsive shared process must not
// terminate it: only a blocked write may.
func TestDiscoveryTimeoutKeepsSharedProcess(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	a.mu.Lock()
	a.discoveryTimeout = 300 * time.Millisecond
	a.mu.Unlock()
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command + " wait-list"}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	connected(t, a, id)
	before := len(fixtureCalls(t, command))
	if c := discoverDone(t, a, agentID); c.Error == "" || c.Complete {
		t.Fatalf("unanswered listing did not time out: %+v", c)
	}
	if v := findThread(t, a, id); !v.Connected {
		t.Fatal("discovery timeout disconnected the shared process")
	}
	if v := sendDone(t, a, id, "after discovery"); v.Error != "" || lastText(v) != "Echo: after discovery" {
		t.Fatalf("session unusable after discovery timeout: %+v", v)
	}
	if calls := fixtureCalls(t, command)[before:]; count(calls, "initialize") != 0 {
		t.Fatalf("process restarted: %v", calls)
	}
}
