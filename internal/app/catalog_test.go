package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func discoverDone(t *testing.T, a *App, agentID string) Catalog {
	t.Helper()
	if err := a.Discover(agentID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c := a.Snapshot().Catalog
		if !c.Loading {
			return c
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("discovery timed out")
	return Catalog{}
}

// setRoots edits the fixture agent's own record of a session's directories.
func setRoots(t *testing.T, command, session string, roots []string) {
	t.Helper()
	path := filepath.Join(filepath.Dir(command), "fixture-sessions", session+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	saved["additionalDirectories"], saved["title"] = roots, "Fixture session"
	if b, err = json.Marshal(saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureCalls(t *testing.T, command string) []string {
	t.Helper()
	// The fixture store is beside its executable, not in Mu's state directory.
	b, err := os.ReadFile(filepath.Join(filepath.Dir(command), "fixture-sessions", "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []string
	// A concurrently appended final line may not be complete yet.
	end := strings.LastIndexByte(string(b), '\n')
	if end < 0 {
		return calls
	}
	for _, line := range strings.Split(string(b[:end]), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m struct{ Method string }
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, m.Method)
	}
	return calls
}

func TestDiscoveryImportAndExternalRemoval(t *testing.T) {
	command, cwd, dir := testutil.Agent(t), t.TempDir(), t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Create three sessions outside Mu. Discovery must follow the fixture's pages.
	p, err := agent.Start(ctx, command, cwd, agent.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var external []string
	additional := []string{t.TempDir()}
	for i := 0; i < 3; i++ {
		result, err := p.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, AdditionalDirectories: additional, McpServers: []acp.McpServer{}})
		if err != nil {
			t.Fatal(err)
		}
		external = append(external, string(result.SessionId))
		if _, err := p.Conn.Prompt(ctx, acp.PromptRequest{SessionId: result.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("external history")}}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: result.SessionId}); err != nil {
			t.Fatal(err)
		}
	}
	p.Close()
	a := openTest(t, dir)
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	before := len(fixtureCalls(t, command))
	c := discoverDone(t, a, agentID)
	if c.Error != "" || !c.Complete || len(c.Sessions) != 3 || len(a.Snapshot().Threads) != 0 {
		t.Fatalf("bad discovery: %+v", c)
	}
	for _, call := range fixtureCalls(t, command)[before:] {
		if call != "initialize" && call != "session/list" {
			t.Fatalf("discovery invoked %s", call)
		}
	}
	ids, err := a.Import(c.Generation, []string{external[0]})
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	id := ids[0]
	if roots := findThread(t, a, id).AdditionalDirectories; len(roots) != 1 || roots[0] != additional[0] {
		t.Fatal("import lost workspace roots", roots)
	}
	if len(a.Snapshot().Threads) != 1 || len(findThread(t, a, id).Entries) != 0 {
		t.Fatal("import eagerly loaded or added unselected sessions")
	}
	duplicate, err := a.Import(c.Generation, []string{external[0], external[0]})
	if err != nil || len(a.Snapshot().Threads) != 1 || duplicate[0] != id {
		t.Fatal("duplicate import", err)
	}
	v := connected(t, a, id)
	if len(v.Entries) != 2 || lastText(v) != "Echo: external history" {
		t.Fatal("import did not load agent history", v)
	}
	// Different listed additional directories do not invalidate the reference
	// or expand the roots accepted at import.
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	setRoots(t, command, external[0], []string{t.TempDir()})
	c = discoverDone(t, a, agentID)
	if v := findThread(t, a, id); c.Error != "" || !slices.Equal(v.AdditionalDirectories, additional) || v.Title != "Fixture session" || !strings.Contains(v.Notice, "additional directories") {
		t.Fatalf("changed additional directories mishandled: %+v", v)
	}
	setRoots(t, command, external[0], additional)
	if err := a.Archive(id, false); err != nil {
		t.Fatal(err)
	}
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if err := os.Remove(filepath.Join(filepath.Dir(command), "fixture-sessions", external[0]+".json")); err != nil {
		t.Fatal(err)
	}
	c = discoverDone(t, a, agentID)
	if c.Error != "" || len(c.Sessions) != 2 || len(a.Snapshot().Threads) != 1 || findThread(t, a, id).Notice == "" {
		t.Fatal("absence silently removed reference", c)
	}
	if err := a.Archive(id, false); err != nil {
		t.Fatal(err)
	}
	before = len(fixtureCalls(t, command))
	if err := a.Connect(id); err != nil {
		t.Fatal(err)
	}
	v = await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if !strings.Contains(v.SessionError, "session not found") || v.SessionID != external[0] || v.Connected {
		t.Fatal("missing session replaced or error hidden", v)
	}
	for _, call := range fixtureCalls(t, command)[before:] {
		if call == "session/new" || call == "session/prompt" {
			t.Fatalf("missing-session fallback: %s", call)
		}
	}
	if err := a.Delete(id); err != nil {
		t.Fatal(err)
	}
	if len(a.Snapshot().Threads) != 0 {
		t.Fatal("remove did not remove reference")
	}
	// No agent-side deletion is part of removal.
	for _, call := range fixtureCalls(t, command) {
		if call == "session/delete" {
			t.Fatal("local removal deleted agent history")
		}
	}
}

func TestDiscoveryErrorsAndStaleImport(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command + " repeat-cursor"}, true)
	if err != nil {
		t.Fatal(err)
	}
	c := discoverDone(t, a, agentID)
	if c.Error == "" || c.Complete {
		t.Fatal("looping pagination accepted", c)
	}
	if _, err := a.Import(c.Generation, []string{"anything"}); err == nil {
		t.Fatal("import from incomplete catalog")
	}
	_, err = a.SaveAgent(state.AgentConfig{ID: agentID, Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	c = discoverDone(t, a, agentID)
	if c.Error != "" || !c.Complete {
		t.Fatal(c)
	}
	if _, err := a.Import(c.Generation-1, nil); err == nil {
		t.Fatal("stale generation accepted")
	}
	_, err = a.SaveAgent(state.AgentConfig{ID: agentID, Name: "Fixture", Command: command + " session-ui"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Import(c.Generation, nil); err == nil {
		t.Fatal("retargeted cached discovery to new command")
	}
}

func TestReloadReplacesProjectionAndNativeForkUsesSourceConnection(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	first := sendDone(t, a, id, "first")
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if v.SessionError != "" || len(v.Entries) != 2 || lastText(v) != "Echo: first" || v.SessionID != first.SessionID {
		t.Fatal("reload appended instead of replaced", v)
	}
	// Mutating a snapshot cannot corrupt replayed history.
	v.Entries[0].Text = "corrupt"
	if findThread(t, a, id).Entries[0].Text != "first" {
		t.Fatal("aliased snapshot")
	}
	before := len(fixtureCalls(t, command))
	fork, err := a.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	// One session/fork on the source's own connection, then the fork is
	// released there. No extra process, resume, replay or prompt, and the
	// source session is not closed.
	calls := fixtureCalls(t, command)[before:]
	if !slices.Equal(calls, []string{"session/fork", "session/close"}) {
		t.Fatal("unexpected fork lifecycle", calls)
	}
	s := a.Snapshot()
	if s.LifecycleError != "" {
		t.Fatal("fork reported a lifecycle error:", s.LifecycleError)
	}
	source := findThread(t, a, id)
	if !source.Connected || source.Configuring || source.SessionError != "" || source.SessionID != first.SessionID {
		t.Fatal("fork disturbed the source thread", source)
	}
	if v := sendDone(t, a, id, "after fork"); lastText(v) != "Echo: after fork" {
		t.Fatal("source unusable after fork", v)
	}
	if findThread(t, a, fork).SessionID == first.SessionID {
		t.Fatal("fork reused session ID")
	}
	if v := sendDone(t, a, fork, "in fork"); len(v.Entries) != 4 || v.Entries[0].Text != "first" || lastText(v) != "Echo: in fork" {
		t.Fatal("fork did not load agent history", v.Entries)
	}
}

func TestForkConnectsDisconnectedSource(t *testing.T) {
	dir := t.TempDir()
	command := testutil.Agent(t)
	a := openTest(t, dir)
	id := createTest(t, a, command)
	sendDone(t, a, id, "before restart")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	if findThread(t, a, id).Connected {
		t.Fatal("source unexpectedly connected")
	}
	fork, err := a.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	if s := a.Snapshot(); s.LifecycleError != "" {
		t.Fatal(s.LifecycleError)
	}
	if source := findThread(t, a, id); !source.Connected || len(source.Entries) != 2 {
		t.Fatal("source was not connected and loaded for the fork", source)
	}
	if v := sendDone(t, a, fork, "branch"); len(v.Entries) != 4 || v.Entries[0].Text != "before restart" {
		t.Fatal("fork lost context", v.Entries)
	}
}

func TestFailedReplayLeavesPreviousProjection(t *testing.T) {
	command := testutil.Agent(t) + " partial-load-fail"
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	sendDone(t, a, id, "kept history")
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if v.SessionError == "" || len(v.Entries) != 2 || lastText(v) != "Echo: kept history" {
		t.Fatal("partial replay committed", v)
	}
}
