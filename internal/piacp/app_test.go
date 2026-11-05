package piacp_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/builtins"
	"github.com/spachava753/mu/internal/state"
)

func openApp(t *testing.T, e *env, data string) *app.App {
	t.Helper()
	a, err := app.Open(data, e.work, builtins.Pi(e.state))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// waitPi waits for Mu's check of the built-in Pi.
func waitPi(t *testing.T, a *app.App, want app.BuiltinState) app.BuiltinView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		b := a.Snapshot().Builtins[0]
		if !b.Checking && b.Status.State == want {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("Pi's state: %+v, want %s", b.Status, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func threadOf(a *app.App, id string) app.ThreadView {
	for _, thread := range a.Snapshot().Threads {
		if thread.ID == id {
			return thread
		}
	}
	return app.ThreadView{}
}

func waitThread(t *testing.T, a *app.App, id, what string, condition func(app.ThreadView) bool) app.ThreadView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		thread := threadOf(a, id)
		if condition(thread) {
			return thread
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %+v", what, thread)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func idle(thread app.ThreadView) bool {
	return thread.Connected && !thread.Running && !thread.Configuring
}

func assistantText(thread app.ThreadView) string {
	var out strings.Builder
	for _, entry := range thread.Entries {
		if entry.Role == "assistant" {
			out.WriteString(entry.Text)
		}
	}
	return out.String()
}

// The adapter through all of Mu: the built-in agent, found where the user says it
// is, used the way the app uses any agent, across a restart of the app.
func TestAdapterThroughMu(t *testing.T) {
	e := newEnv(t)
	data := t.TempDir()
	a := openApp(t, e, data)
	defer func() { _ = a.Close() }()
	if err := a.CheckBuiltin("pi", filepath.Join(e.work, "missing")); err != nil {
		t.Fatal(err)
	}
	waitPi(t, a, app.BuiltinNotFound)
	if err := a.CheckBuiltin("pi", fakePi(t)); err != nil {
		t.Fatal(err)
	}
	if b := waitPi(t, a, app.BuiltinReady); b.Status.Version != "1.5.0" || a.Snapshot().DefaultAgentID != b.ID() {
		t.Fatalf("Pi %+v, default %q", b, a.Snapshot().DefaultAgentID)
	}
	agentID := a.Snapshot().Builtins[0].ID() // Opening a thread creates its session, with the agent's options ready.
	id, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(id); err != nil {
		t.Fatal(err)
	}
	thread := waitThread(t, a, id, "a connection", idle)
	if len(thread.Session.Options) != 1 || thread.Session.Options[0].Name != "Model" {
		t.Fatalf("options %+v", thread.Session.Options)
	}
	if !slices.ContainsFunc(thread.Session.Commands, func(c acp.AvailableCommand) bool { return c.Name == "compact" }) {
		t.Fatalf("commands %+v", thread.Session.Commands)
	}

	// A plain turn, then one with a tool.
	if err := a.Send(id, "hello"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, a, id, "the reply", func(v app.ThreadView) bool { return idle(v) && assistantText(v) == "Hello from Pi." })
	if err := a.Send(id, "use-a-tool"); err != nil {
		t.Fatal(err)
	}
	thread = waitThread(t, a, id, "the tool turn", func(v app.ThreadView) bool { return idle(v) && strings.HasSuffix(assistantText(v), "Done.") })
	i := slices.IndexFunc(thread.Entries, func(entry state.Entry) bool { return entry.Role == "tool" })
	if i < 0 || thread.Entries[i].Status != "completed" || !strings.Contains(thread.Entries[i].Text, "there") {
		t.Fatalf("tool entry %+v", thread.Entries)
	}
	if thread.Session.Usage == nil || thread.Session.Usage.Size != 100000 {
		t.Fatalf("usage %+v", thread.Session.Usage)
	}

	// A failed turn is shown and doesn't detach the session.
	if err := a.Send(id, "fail-now"); err != nil {
		t.Fatal(err)
	}
	thread = waitThread(t, a, id, "the failure", func(v app.ThreadView) bool { return idle(v) && v.Error != "" })
	if !strings.Contains(thread.Error, "the model refused") {
		t.Fatalf("error %q", thread.Error)
	}

	// Stop.
	if err := a.Send(id, "run-slowly"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, a, id, "the turn to stream", func(v app.ThreadView) bool { return v.Running && strings.HasSuffix(assistantText(v), "Working") })
	a.Stop(id)
	waitThread(t, a, id, "the stop", idle)

	// Fork, whose history loads when it's opened.
	forked, err := a.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(forked); err != nil {
		t.Fatal(err)
	}
	waitThread(t, a, forked, "the fork's history", func(v app.ThreadView) bool { return idle(v) && strings.Contains(assistantText(v), "Hello from Pi.") })

	// A thread that was never sent to.
	unused, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(unused); err != nil {
		t.Fatal(err)
	}
	waitThread(t, a, unused, "the unused thread", idle)

	// Discovery lists them all, and a complete listing marks none missing.
	if err := a.Discover(agentID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for !a.Snapshot().Catalog.Complete {
		if time.Now().After(deadline) {
			t.Fatalf("discovery: %+v", a.Snapshot().Catalog)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, thread := range a.Snapshot().Threads {
		if strings.Contains(thread.Notice, "not listed") {
			t.Fatalf("thread %q marked missing: %s", thread.Title, thread.Notice)
		}
	}
	if got := len(a.Snapshot().Catalog.Sessions); got != 3 {
		t.Fatalf("discovered %d sessions: %+v", got, a.Snapshot().Catalog.Sessions)
	}

	// Restart: history comes back from the agent, and the unused thread reopens.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openApp(t, e, data)
	defer func() { _ = a.Close() }()
	waitPi(t, a, app.BuiltinReady) // The path was saved.
	if err := a.Connect(id); err != nil {
		t.Fatal(err)
	}
	waitThread(t, a, id, "replay", func(v app.ThreadView) bool { return idle(v) && strings.Contains(assistantText(v), "Hello from Pi.") })
	if err := a.Connect(unused); err != nil {
		t.Fatal(err)
	}
	thread = waitThread(t, a, unused, "the unused thread to reopen", func(v app.ThreadView) bool { return idle(v) || v.SessionError != "" })
	if thread.SessionError != "" {
		t.Fatalf("unused thread: %s", thread.SessionError)
	}
	if err := a.Send(unused, "hello"); err != nil {
		t.Fatal(err)
	}
	waitThread(t, a, unused, "a first turn after reopening", func(v app.ThreadView) bool { return idle(v) && assistantText(v) == "Hello from Pi." })
}
