package app

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

// stubAgent is a built-in whose checks do what a test says.
type stubAgent struct {
	checks atomic.Int64
	detect atomic.Pointer[func(ctx context.Context, executable string) BuiltinStatus]
}

func newStubAgent(status BuiltinState) *stubAgent {
	s := &stubAgent{}
	s.answer(status)
	return s
}

func (s *stubAgent) answer(status BuiltinState) {
	detect := func(context.Context, string) BuiltinStatus { return BuiltinStatus{State: status} }
	s.detect.Store(&detect)
}

func (s *stubAgent) builtin() Builtin {
	return Builtin{
		Name: "stub", Title: "Stub",
		Detect: func(ctx context.Context, executable, _ string) BuiltinStatus {
			s.checks.Add(1)
			return (*s.detect.Load())(ctx, executable)
		},
		Serve: func(context.Context, agent.Link, func() string) error { return nil },
	}
}

const stubID = builtinIDPrefix + "stub"

func openStub(t *testing.T, dir string, s *stubAgent) *App {
	t.Helper()
	a, err := Open(dir, t.TempDir(), s.builtin())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func waitStub(t *testing.T, a *App, want BuiltinState) BuiltinView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b := a.Snapshot().Builtins[0]
		if !b.Checking && b.Status.State == want {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %+v, want %q", b, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// A check starts a process and can take as long as it likes. Nothing the UI thread
// calls may wait for it.
func TestChecksNeverBlockTheCaller(t *testing.T) {
	s := newStubAgent(BuiltinReady)
	release := make(chan struct{})
	blocked := func(ctx context.Context, _ string) BuiltinStatus {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return BuiltinStatus{State: BuiltinReady}
	}
	s.detect.Store(&blocked)
	a := openStub(t, t.TempDir(), s)
	defer close(release)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			if b := a.Snapshot().Builtins[0]; !b.Checking {
				t.Error("no sign of the check in progress")
			}
			a.RefreshBuiltins()
			if err := a.CheckBuiltin("stub", ""); err != nil {
				t.Error(err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the UI's calls waited for a check")
	}
}

func TestCloseEndsACheckInProgress(t *testing.T) {
	s := newStubAgent(BuiltinReady)
	started := make(chan struct{})
	blocked := func(ctx context.Context, _ string) BuiltinStatus {
		close(started)
		<-ctx.Done()
		return BuiltinStatus{}
	}
	s.detect.Store(&blocked)
	a, err := Open(t.TempDir(), t.TempDir(), s.builtin())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close waited for a check")
	}
}

func TestChecksCoalesce(t *testing.T) {
	s := newStubAgent(BuiltinReady)
	entered, release := make(chan struct{}, 10), make(chan struct{})
	gated := func(context.Context, string) BuiltinStatus {
		entered <- struct{}{}
		<-release
		return BuiltinStatus{State: BuiltinReady}
	}
	s.detect.Store(&gated)
	a := openStub(t, t.TempDir(), s)
	<-entered
	for range 20 {
		if err := a.CheckBuiltin("stub", ""); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	waitStub(t, a, BuiltinReady)
	if got := s.checks.Load(); got != 2 {
		t.Fatalf("%d checks, want the one running and one more", got)
	}
}

func TestRefreshSkipsARecentCheck(t *testing.T) {
	s := newStubAgent(BuiltinReady)
	a := openStub(t, t.TempDir(), s)
	waitStub(t, a, BuiltinReady)
	a.RefreshBuiltins()
	waitStub(t, a, BuiltinReady)
	if got := s.checks.Load(); got != 1 {
		t.Fatalf("%d checks", got)
	}
	// Check again always checks.
	if err := a.CheckBuiltin("stub", ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.checks.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("no check")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestExecutableIsSavedAndCheckedAfterItChanges(t *testing.T) {
	s := newStubAgent(BuiltinReady)
	seen := make(chan string, 10)
	report := func(_ context.Context, executable string) BuiltinStatus {
		seen <- executable
		return BuiltinStatus{State: BuiltinReady}
	}
	s.detect.Store(&report)
	dir := t.TempDir()
	a, err := Open(dir, t.TempDir(), s.builtin())
	if err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "" {
		t.Fatalf("first check of %q", got)
	}
	if err := a.CheckBuiltin("stub", "  /opt/stub  "); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "/opt/stub" {
		t.Fatalf("check of %q", got)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openStub(t, dir, s)
	if got := <-seen; got != "/opt/stub" {
		t.Fatalf("after reopening, check of %q", got)
	}
	if got := a.Snapshot().Builtins[0].Executable; got != "/opt/stub" {
		t.Fatalf("saved %q", got)
	}
	if err := a.CheckBuiltin("stub", ""); err != nil {
		t.Fatal(err)
	}
	if got := a.Snapshot().Builtins[0].Executable; got != "" {
		t.Fatalf("cleared to %q", got)
	}
	if err := a.CheckBuiltin("other", ""); err == nil {
		t.Fatal("checked an agent that doesn't exist")
	}
}

func TestDefaultAgent(t *testing.T) {
	s := newStubAgent(BuiltinReady)
	a := openStub(t, t.TempDir(), s)
	waitStub(t, a, BuiltinReady)

	// With nothing else, a found built-in is the default.
	if got := a.Snapshot().DefaultAgentID; got != stubID {
		t.Fatalf("default %q", got)
	}
	// Adding an agent doesn't take that away, unless asked.
	custom, err := a.SaveAgent(state.AgentConfig{Name: "Custom", Command: "custom"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Snapshot().DefaultAgentID; got != stubID {
		t.Fatalf("default after adding an agent %q", got)
	}
	// When it isn't found, the next best stands in, and the choice is kept.
	s.answer(BuiltinNotFound)
	if err := a.CheckBuiltin("stub", ""); err != nil {
		t.Fatal(err)
	}
	waitStub(t, a, BuiltinNotFound)
	if got := a.Snapshot().DefaultAgentID; got != custom {
		t.Fatalf("default with the built-in missing %q", got)
	}
	s.answer(BuiltinReady)
	if err := a.CheckBuiltin("stub", ""); err != nil {
		t.Fatal(err)
	}
	waitStub(t, a, BuiltinReady)
	if got := a.Snapshot().DefaultAgentID; got != stubID {
		t.Fatalf("default after it returned %q", got)
	}
}

// A detected built-in doesn't take over from an agent that's already the default.
func TestFoundBuiltinDoesNotTakeOverTheDefault(t *testing.T) {
	dir := t.TempDir()
	first := newStubAgent(BuiltinNotFound)
	a, err := Open(dir, t.TempDir(), first.builtin())
	if err != nil {
		t.Fatal(err)
	}
	waitStub(t, a, BuiltinNotFound)
	custom, err := a.SaveAgent(state.AgentConfig{Name: "Custom", Command: "custom"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Snapshot().DefaultAgentID; got != custom {
		t.Fatalf("default %q", got)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openStub(t, dir, newStubAgent(BuiltinReady))
	waitStub(t, a, BuiltinReady)
	if got := a.Snapshot().DefaultAgentID; got != custom {
		t.Fatalf("default %q", got)
	}
}

func TestThreadsWithABuiltin(t *testing.T) {
	a := openStub(t, t.TempDir(), newStubAgent(BuiltinReady))
	waitStub(t, a, BuiltinReady)
	id, err := a.CreateWithAgent(stubID, "")
	if err != nil {
		t.Fatal(err)
	}
	s := a.Snapshot()
	var created ThreadView
	for _, thread := range s.Threads {
		if thread.ID == id {
			created = thread
		}
	}
	if created.AgentID != stubID || created.AgentName != "Stub" || created.Command != "mu:stub" || !filepath.IsAbs(created.Cwd) {
		t.Fatalf("thread %+v", created.Thread)
	}
	// A built-in's command isn't one to offer for typing again.
	if s.LastCommand != "" {
		t.Fatalf("last command %q", s.LastCommand)
	}
	if _, err := a.CreateWithAgent(builtinIDPrefix+"other", ""); err == nil {
		t.Fatal("created a thread with an agent that doesn't exist")
	}
}

func TestOfferedAndUsable(t *testing.T) {
	for state, want := range map[BuiltinState][2]bool{
		BuiltinUnknown:   {true, false},
		BuiltinReady:     {true, true},
		BuiltinSignedOut: {true, true},
		BuiltinOutdated:  {true, false},
		BuiltinBroken:    {true, false},
		BuiltinNotFound:  {false, false},
	} {
		v := BuiltinView{Status: BuiltinStatus{State: state}}
		if v.Offered() != want[0] || v.Usable() != want[1] {
			t.Errorf("%q: offered %v, usable %v", state, v.Offered(), v.Usable())
		}
	}
}

func TestToolInputShowsTheOneText(t *testing.T) {
	for name, test := range map[string]struct {
		raw  any
		want string
	}{
		"nothing":    {nil, ""},
		"command":    {map[string]any{"command": "go test\n\t./...", "timeout": 10.0}, "go test\n    ./..."},
		"script":     {map[string]any{"code": "a()\nb()"}, "a()\nb()"},
		"several":    {map[string]any{"a": "x", "b": "y"}, "{\n  \"a\": \"x\",\n  \"b\": \"y\"\n}"},
		"not a map":  {[]any{"x"}, "[\n  \"x\"\n]"},
		"no strings": {map[string]any{"n": 1.0}, "{\n  \"n\": 1\n}"},
	} {
		if got := toolInput(test.raw); got != test.want {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
	}
}

// Code is mostly indented with tabs, and the text renderer drops them.
func TestToolTextAndDiffsKeepIndentation(t *testing.T) {
	old := "\tone\n"
	content := []acp.ToolCallContent{
		{Type: acp.ToolCallContentTypeContent, Content: acp.TextContentBlock("\tout")},
		{Type: acp.ToolCallContentTypeDiff, Path: "/f", OldText: &old, NewText: "\ttwo\n"},
	}
	if got := toolText(content, nil); got != "    out\n" {
		t.Fatalf("text %q", got)
	}
	files := toolDiffs(content)
	if len(files) != 1 || files[0].Hunks[0][0].Text != "    one" || files[0].Hunks[0][1].Text != "    two" {
		t.Fatalf("diffs %+v", files)
	}
}
