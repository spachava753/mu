package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

// A user's title stays: it survives agent updates, discovery, the first
// message, and restarts.
func TestRenameIsKept(t *testing.T) {
	command, dir := testutil.Agent(t), t.TempDir()
	a := openTest(t, dir)
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command + " no-title"}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "   \n "} {
		if err := a.Rename(id, bad); err == nil {
			t.Fatalf("accepted title %q", bad)
		}
	}
	if err := a.Rename("no-such-thread", "x"); err == nil {
		t.Fatal("renamed a missing thread")
	}
	// Whitespace collapses; a title the first message would have replaced stays.
	if err := a.Rename(id, "  My   thread\n"); err != nil {
		t.Fatal(err)
	}
	v := sendDone(t, a, id, "updates") // Reports a session_info_update title.
	if v.Title != "My thread" || !v.Renamed {
		t.Fatalf("title replaced: %q", v.Title)
	}
	if c := discoverDone(t, a, agentID); c.Error != "" {
		t.Fatal(c)
	}
	if got := findThread(t, a, id).Title; got != "My thread" {
		t.Fatalf("discovery replaced the title: %q", got)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openTest(t, dir)
	if v := findThread(t, b, id); v.Title != "My thread" || !v.Renamed {
		t.Fatalf("title not persisted: %+v", v.Thread)
	}
}

// A failed save leaves the old title.
func TestRenameRollsBackOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	id := createTest(t, a, "unused-agent")
	path := filepath.Join(dir, "threads.json")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path); _ = os.Rename(path+".saved", path) }()
	if err := a.Rename(id, "Unsaved"); err == nil {
		t.Fatal("failed save accepted")
	}
	if v := findThread(t, a, id); v.Title != "New thread" || v.Renamed {
		t.Fatalf("title kept after failed save: %+v", v.Thread)
	}
}

// An agent that stores titles is told, and its stored title is listed.
func TestRenameIsStoredByAgent(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	session := connected(t, a, id).SessionID
	if err := a.Rename(id, "Stored title"); err != nil {
		t.Fatal(err)
	}
	waitCall(t, command, "session/set_title")
	c := discoverDone(t, a, agentID)
	if c.Error != "" || len(c.Sessions) != 1 || c.Sessions[0].ID != session || c.Sessions[0].Title != "Stored title" {
		t.Fatalf("agent did not store the title: %+v", c)
	}
}

// Agents that do not store titles get no request, and one with no running
// process does not start for a rename.
func TestRenameStaysLocalWithoutAgentSupport(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command+" no-title")
	connected(t, a, id)
	before := len(fixtureCalls(t, command))
	if err := a.Rename(id, "Local title"); err != nil {
		t.Fatal(err)
	}
	if v := findThread(t, a, id); v.Title != "Local title" {
		t.Fatal(v.Title)
	}
	stopped := createTest(t, a, command) // Never connected: no process, no session.
	if err := a.Rename(stopped, "Also local"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if calls := fixtureCalls(t, command)[before:]; len(calls) != 0 {
		t.Fatalf("rename reached the agent: %v", calls)
	}
}

// If the agent refuses, the title stays in Mu and the failure is shown until a
// later rename succeeds.
func TestRenameAgentFailureKeepsLocalTitle(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" title-error")
	connected(t, a, id)
	if err := a.Rename(id, "Refused"); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return v.TitleError != "" })
	if v.Title != "Refused" || !strings.Contains(v.TitleError, "kept in Mu") || v.SessionError != "" {
		t.Fatalf("%+v", v)
	}
	if err := a.Rename(id, "Accepted"); err != nil {
		t.Fatal(err)
	}
	if v := findThread(t, a, id); v.TitleError != "" {
		t.Fatal("old failure outlived a new rename:", v.TitleError)
	}
	time.Sleep(100 * time.Millisecond)
	if v := findThread(t, a, id); v.TitleError != "" || v.Title != "Accepted" {
		t.Fatalf("%+v", v)
	}
}

// An unchanged title is not a rename: the thread keeps following the agent
// and its first message.
func TestRenameToSameTitleChangesNothing(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	before := len(fixtureCalls(t, command))
	if err := a.Rename(id, " New  thread "); err != nil {
		t.Fatal(err)
	}
	if v := findThread(t, a, id); v.Renamed {
		t.Fatal("unchanged title pinned the thread")
	}
	if v := sendDone(t, a, id, "hello"); v.Title != "hello" {
		t.Fatalf("first message did not name the thread: %q", v.Title)
	}
	if calls := fixtureCalls(t, command)[before:]; slices.Contains(calls, "session/set_title") {
		t.Fatalf("unchanged title reached the agent: %v", calls)
	}
}

// Overlapping renames reach the agent in order, coalesced, and the agent ends
// with the latest title.
func TestOverlappingRenamesEndWithLatest(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command + " slow-title"}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	connected(t, a, id)
	before := len(fixtureCalls(t, command))
	if err := a.Rename(id, "First"); err != nil {
		t.Fatal(err)
	}
	waitCall(t, command, "session/set_title") // First is in flight.
	for _, title := range []string{"Second", "Third"} {
		if err := a.Rename(id, title); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		syncing := a.runtimes[id].titleSyncing
		a.mu.Unlock()
		if !syncing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("title worker did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := count(fixtureCalls(t, command)[before:], "session/set_title"); n != 2 {
		t.Fatalf("expected the first and the coalesced latest request, got %d", n)
	}
	c := discoverDone(t, a, agentID)
	if c.Error != "" || len(c.Sessions) != 1 || c.Sessions[0].Title != "Third" || findThread(t, a, id).Title != "Third" {
		t.Fatalf("agent or thread did not end with the latest title: %+v", c.Sessions)
	}
}

// An outcome for a superseded rename is ignored, and a title that returns to
// the same text (A, B, A) is requested again rather than matched to the first.
func TestStaleTitleOutcomeIsIgnored(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command+" slow-title title-error")
	connected(t, a, id)
	synced := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			a.mu.Lock()
			syncing := a.runtimes[id].titleSyncing
			a.mu.Unlock()
			if !syncing {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("title worker did not finish")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	before := len(fixtureCalls(t, command))
	if err := a.Rename(id, "Refused"); err != nil {
		t.Fatal(err)
	}
	waitCall(t, command, "session/set_title") // The refusal is on its way.
	if err := a.Rename(id, "Accepted"); err != nil {
		t.Fatal(err)
	}
	synced()
	if v := findThread(t, a, id); v.TitleError != "" || v.Title != "Accepted" {
		t.Fatalf("stale failure applied: %+v", v)
	}
	if n := count(fixtureCalls(t, command)[before:], "session/set_title"); n != 2 {
		t.Fatalf("requests: %d", n)
	}
	// A, B, A: the first request's refusal is stale, the last one's is not.
	for _, title := range []string{"Refused", "Other", "Refused"} {
		if err := a.Rename(id, title); err != nil {
			t.Fatal(err)
		}
	}
	synced()
	if v := findThread(t, a, id); v.TitleError == "" || v.Title != "Refused" {
		t.Fatalf("latest outcome not applied: %+v", v)
	}
	a.DismissTitleError(id)
	if v := findThread(t, a, id); v.TitleError != "" {
		t.Fatal("dismiss kept the error")
	}
}

// A title request has no deadline, so that it cannot be overtaken, but it does
// not hold up shutdown.
func TestShutdownEndsUnansweredTitleRequest(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command+" wait-title")
	connected(t, a, id)
	if err := a.Rename(id, "Never answered"); err != nil {
		t.Fatal(err)
	}
	waitCall(t, command, "session/set_title")
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for the title request")
	}
}

// A title update from an agent still applies to a thread the user has not
// renamed.
func TestAgentTitleAppliesWithoutRename(t *testing.T) {
	a, id, r := protocolRuntime(t)
	title := "From the agent"
	info := acp.SessionInfoUpdateSessionUpdate()
	info.Title = &title
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: info})
	if got := findThread(t, a, id).Title; got != title {
		t.Fatal(got)
	}
}
