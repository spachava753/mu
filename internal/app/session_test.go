package app

import (
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/testutil"
)

func connected(t *testing.T, a *App, id string) ThreadView {
	t.Helper()
	if err := a.Connect(id); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if !v.Connected || v.SessionError != "" {
		t.Fatalf("could not connect: %+v", v)
	}
	return v
}

func TestSessionOptionsAndAllUpdates(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	id := createTest(t, a, testutil.Agent(t)+" session-ui")
	v := connected(t, a, id)
	if len(v.Entries) != 0 {
		t.Fatal("connecting sent a prompt")
	}
	if len(v.Session.Options) != 3 || v.Session.Options[0].CurrentValue != "fast" {
		t.Fatal("setup options missing", v.Session)
	}
	if len(v.Session.Commands) != 1 || v.Session.Usage == nil || v.Session.Usage.Used != 12000 {
		t.Fatal("lost notifications sent before session/new response", v.Session)
	}
	if err := a.SetOption(id, "model", "not-offered"); err == nil {
		t.Fatal("invalid choice accepted")
	}
	if err := a.SetOption(id, "model", "deep"); err != nil {
		t.Fatal(err)
	}
	v = await(t, a, id, func(v ThreadView) bool { return !v.Configuring && v.Session.Usage.Used == 16000 })
	if v.Session.Options[0].CurrentValue != "deep" || v.Session.Options[1].CurrentValue != "high" {
		t.Fatal("dependent options were not replaced", v.Session)
	}
	if err := a.SetOption(id, "enabled", true); err != nil {
		t.Fatal(err)
	}
	v = await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if v.Session.Options[2].CurrentValue != true {
		t.Fatal("boolean option not acknowledged")
	}
	if err := a.SetOption(id, "model", "reject"); err != nil {
		t.Fatal(err)
	}
	v = await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if v.SessionError == "" || v.Session.Options[0].CurrentValue != "deep" {
		t.Fatal("failed update was applied or error hidden")
	}
	v = sendDone(t, a, id, "updates")
	if v.Title != "Session controls and usage" || v.Session.AgentUpdatedAt == "" {
		t.Fatal("metadata update missing")
	}
	if v.Session.TurnUsage == nil || v.Session.TurnUsage.TotalTokens != 30 || *v.Session.TurnUsage.CachedWriteTokens != 2 {
		t.Fatal("turn usage lost", v.Session)
	}
	if v.Session.Modes.CurrentModeID != "code" || v.Session.Options[0].CurrentValue != "deep" {
		t.Fatal("mode/config updates lost")
	}
	if len(v.Session.Metadata) == 0 {
		t.Fatal("extension metadata discarded")
	}
	users, legacyPlans, thoughts, tools := 0, 0, 0, 0
	for _, e := range v.Entries {
		switch e.Role {
		case "user":
			users++
		case "thought":
			thoughts++
		case "tool":
			tools++
			if e.Status != "completed" || !strings.Contains(e.Details, "rawInput") || !strings.Contains(e.Details, "locations") || !strings.Contains(e.Details, "rawOutput") {
				t.Fatal("tool update discarded fields", e)
			}
		}
		if e.PlanID == "legacy" {
			legacyPlans++
			if strings.Contains(e.Text, "pending") || !strings.Contains(e.Text, "completed") {
				t.Fatal("plan appended rather than replaced", e)
			}
		}
		if e.PlanID == "discard" {
			t.Fatal("plan removal ignored")
		}
	}
	if users != 1 || thoughts != 1 || tools != 1 || legacyPlans != 1 {
		t.Fatalf("updates missing or prompt echo duplicated: user=%d thought=%d tool=%d plan=%d", users, thoughts, tools, legacyPlans)
	}
	// Snapshots share a read-only deep copy; even a caller that modifies it
	// cannot reach the application's nested SDK values.
	v.Session.Options[0].Name = "corrupted"
	v.Session.Metadata["test"] = []byte(`true`)
	a.mu.Lock()
	r := a.runtimes[id]
	_, aliased := r.Session.Metadata["test"]
	if r.Session.Options[0].Name == "corrupted" || aliased {
		a.mu.Unlock()
		t.Fatal("snapshot aliases live session state")
	}
	r.cloneVersion = 0 // Discard the copy this test modified.
	a.mu.Unlock()
	before := len(v.Entries)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	v = findThread(t, a, id)
	if v.Session.Usage != nil || v.Session.TurnUsage != nil || len(v.Entries) != 0 {
		t.Fatal("transient session state was persisted")
	}
	v = connected(t, a, id)
	if len(v.Entries) != before {
		t.Fatal("session/load replay duplicated history")
	}
}

func TestLegacyModes(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" legacy-options")
	v := connected(t, a, id)
	if len(v.Session.Options) != 0 || v.Session.Modes == nil {
		t.Fatal(v.Session)
	}
	if err := a.SetMode(id, "code"); err != nil {
		t.Fatal(err)
	}
	v = await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if v.Session.Modes.CurrentModeID != "code" {
		t.Fatal("mode change lost")
	}
}

func TestNativeForkAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	id := createTest(t, a, testutil.Agent(t)+" session-ui")
	sendDone(t, a, id, "remember original")
	fork, err := a.Fork(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(findThread(t, a, fork).Entries) != 0 {
		t.Fatal("copied local transcript")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	v := sendDone(t, a, fork, "continue")
	if len(v.Entries) != 4 || v.Entries[0].Text != "remember original" || lastText(v) != "Echo: continue" {
		t.Fatal("native fork did not load agent history", v.Entries)
	}
}

func TestOptionsDuringTurn(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" session-ui")
	connected(t, a, id)
	if err := a.Send(id, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	if err := a.SetOption(id, "model", "deep"); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if !v.Running || v.Session.Options[0].CurrentValue != "deep" {
		t.Fatal("option change interfered with running turn")
	}
	a.Stop(id)
	await(t, a, id, func(v ThreadView) bool { return !v.Running })
}

func TestPendingSettingsProtectLifecycleAndClose(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" session-ui wait-config")
	connected(t, a, id)
	if err := a.SetOption(id, "model", "deep"); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(id, "test"); err == nil {
		t.Fatal("sent during pending settings request")
	}
	if err := a.Archive(id, true); err == nil {
		t.Fatal("archived during pending settings request")
	}
	if _, err := a.Fork(id); err == nil {
		t.Fatal("forked during pending settings request")
	}
	if err := a.Delete(id); err == nil {
		t.Fatal("deleted during pending settings request")
	}
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel pending settings request")
	}
}

func TestIdleUpdatesAndStaleConnections(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, "agent")
	a.mu.Lock()
	r := a.runtimeLocked(id)
	r.session, r.epoch, r.acceptUpdates = "s", 2, true
	a.mu.Unlock()
	u := acp.SessionNotification{SessionID: "s", Update: acp.UsageUpdateSessionUpdate(5, 10)}
	receive := func(epoch uint64, n acp.SessionNotification) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.receiveLocked(id, r, epoch, n)
	}
	receive(1, u)
	if findThread(t, a, id).Session.Usage != nil {
		t.Fatal("old process updated a reconnected session")
	}
	receive(2, u)
	if findThread(t, a, id).Session.Usage.Used != 5 {
		t.Fatal("idle update dropped")
	}
	receive(2, acp.SessionNotification{SessionID: "s", Update: acp.UserMessageChunkSessionUpdate(acp.TextContentBlock("agent-originated user message"))})
	if lastText(findThread(t, a, id)) != "agent-originated user message" {
		t.Fatal("live user message update dropped")
	}
}
