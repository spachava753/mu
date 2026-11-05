package desktop_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/builtins"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/state"
)

// builtinFixture is the real view and app with the real adapter, running the fake
// Pi. PATH is emptied, so that nothing here can reach an installed Pi.
type builtinFixture struct {
	t    *testing.T
	a    *app.App
	view *desktop.View
	tt   *ui.Tester
	pi   string
}

func buildFakePi(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pi")
	cmd := exec.Command("go", "build", "-o", path, "../piacp/testdata/fakepi")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake pi: %v\n%s", err, out)
	}
	return path
}

func newBuiltinFixture(t *testing.T, pi string, width, height int, customAgent bool) *builtinFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("FAKEPI_SESSIONS", filepath.Join(home, "sessions"))
	a, err := app.Open(t.TempDir(), filepath.Join(home, "mu"), builtins.Pi(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if customAgent {
		if _, err := a.SaveAgent(state.AgentConfig{Name: "My custom agent", Command: "my-agent --stdio"}, false); err != nil {
			t.Fatal(err)
		}
	}
	f := &builtinFixture{t: t, a: a, pi: pi}
	f.setPi(pi, app.BuiltinReady)
	f.view = desktop.New(a, "")
	f.tt = ui.NewTester(f.view.Draw, width, height)
	f.tt.Frame()
	f.settle()
	return f
}

func (f *builtinFixture) settle() {
	f.tt.Frame()
	f.tt.Advance(320 * time.Millisecond)
}

// setPi changes what Mu finds and waits until it has looked.
func (f *builtinFixture) setPi(executable string, want app.BuiltinState) {
	f.t.Helper()
	if err := f.a.CheckBuiltin("pi", executable); err != nil {
		f.t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		b := f.a.Snapshot().Builtins[0]
		if !b.Checking && b.Status.State == want {
			break
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("Pi: %+v, want %s", b, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if f.tt != nil {
		f.settle()
	}
}

func (f *builtinFixture) shot(name string, expected ...string) {
	f.t.Helper()
	for _, text := range expected {
		if !f.tt.HasText(text) {
			f.t.Fatalf("%s: %q is not on screen. Texts: %q", name, text, f.tt.Texts())
		}
	}
	f.tt.Move(1, 1) // No hover highlight left behind.
	f.tt.Frame()
	screenshot(f.t, f.tt, name)
}

func (f *builtinFixture) notOffered(name string) {
	f.t.Helper()
	if _, ok := f.tt.Find("Pi"); ok {
		f.t.Fatalf("%s: Pi is offered though it wasn't found", name)
	}
}

func (f *builtinFixture) waitThread(what string, condition func(app.ThreadView) bool) {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		f.tt.Frame()
		for _, thread := range f.a.Snapshot().Threads {
			if condition(thread) {
				f.settle()
				return
			}
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s: %+v", what, f.a.Snapshot().Threads)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (f *builtinFixture) send(text string) {
	f.t.Helper()
	click(f.t, f.tt, "Message")
	f.tt.Type(text)
	f.tt.Frame()
	f.tt.Key(0, ui.KeyEnter)
	f.tt.Frame()
}

func idleThread(thread app.ThreadView) bool {
	return thread.Connected && !thread.Running && !thread.Configuring
}

// Checking again outside Settings mustn't forget where the user said Pi is.
func TestCheckAgainKeepsTheSavedExecutable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "somewhere-else")
	f := newBuiltinFixture(t, buildFakePi(t), 1100, 780, false)
	f.setPi(missing, app.BuiltinNotFound)
	f.view = desktop.New(f.a, "")
	f.tt = ui.NewTester(f.view.Draw, 1100, 780)
	f.settle()
	click(t, f.tt, "Check again")
	deadline := time.Now().Add(20 * time.Second)
	for f.a.Snapshot().Builtins[0].Checking {
		if time.Now().After(deadline) {
			t.Fatal("the check didn't finish")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := f.a.Snapshot().Builtins[0].Executable; got != missing {
		t.Fatalf("the saved path became %q", got)
	}
}

// A thread whose built-in can't run says what's wrong, whichever state it's in.
func TestBuiltinThreadStates(t *testing.T) {
	pi := buildFakePi(t)
	f := newBuiltinFixture(t, pi, 1100, 780, false)
	id, err := f.a.CreateWithAgent("builtin:pi", "")
	if err != nil {
		t.Fatal(err)
	}
	f.setPi(filepath.Join(t.TempDir(), "nothing-here"), app.BuiltinNotFound)
	view := desktop.New(f.a, "")
	f.tt = ui.NewTester(view.Draw, 1100, 780)
	f.waitThread("the failure", func(v app.ThreadView) bool { return v.ID == id && v.SessionError != "" && !v.Configuring })
	f.shot("thread-not-found", "Pi wasn't found", "Retry connection", "pi.dev")

	t.Setenv("FAKEPI_NO_MODELS", "1")
	f.setPi(pi, app.BuiltinSignedOut)
	click(t, f.tt, "Retry connection")
	f.waitThread("the connection", func(v app.ThreadView) bool { return v.ID == id && idleThread(v) })
	f.shot("thread-not-signed-in", "Pi needs a model provider", "No session options")
}

func TestBuiltinPiUI(t *testing.T) {
	pi := buildFakePi(t)
	for _, config := range []struct {
		w, h int
		dark bool
	}{{1100, 780, true}, {800, 600, true}, {1100, 780, false}} {
		theme := "light"
		if config.dark {
			theme = "dark"
		}
		suffix := fmt.Sprintf("--%dx%d-%s", config.w, config.h, theme)
		t.Run(suffix[2:], func(t *testing.T) { builtinScreens(t, pi, config.w, config.h, config.dark, suffix) })
	}
}

func builtinScreens(t *testing.T, pi string, width, height int, dark bool, suffix string) {
	missing := filepath.Join(t.TempDir(), "nothing-here")

	// First run: nothing to configure, and without Pi a way to get it.
	f := newBuiltinFixture(t, pi, width, height, false)
	f.tt.SetDark(dark)
	f.settle()
	f.shot("first-run"+suffix, "Ready · version 1.5.0 · built-in adapter", "Create thread")
	if got := f.a.Snapshot().DefaultAgentID; got != "builtin:pi" {
		t.Fatalf("default %q", got)
	}
	f.setPi(missing, app.BuiltinNotFound)
	f.shot("first-run-not-found"+suffix, "Mu couldn't find Pi", "pi.dev", "Agent command")
	f.notOffered("first run")
	click(t, f.tt, "Settings")
	f.tt.SetSize(width, 1560)
	f.settle()
	f.shot("settings-full-first-run-not-found"+suffix, "No custom agents.", "pi.dev", "Coming soon")
	f.tt.SetSize(width, height)
	click(t, f.tt, "Import sessions")
	f.shot("import-first-run-not-found"+suffix, "Save an agent configuration")

	// With a custom agent as well.
	f = newBuiltinFixture(t, pi, width, height, true)
	f.tt.SetDark(dark)
	f.settle()
	click(t, f.tt, "New thread")
	f.shot("new-thread-ready"+suffix, "Ready · version 1.5.0 · built-in adapter")
	click(t, f.tt, "Configured agent")
	f.shot("new-thread-agent-menu"+suffix, "Built-in", "Custom")
	f.tt.Key(0, ui.KeyEscape)
	f.settle()
	f.setPi(missing, app.BuiltinNotFound)
	f.shot("new-thread-not-found"+suffix, "My custom agent", "my-agent --stdio")
	f.notOffered("new thread")
	click(t, f.tt, "Configured agent")
	f.shot("new-thread-agent-menu-not-found"+suffix, "My custom agent")
	f.notOffered("new thread menu")
	f.tt.Key(0, ui.KeyEscape)
	f.settle()

	click(t, f.tt, "Settings")
	f.tt.SetSize(width, 1560)
	f.settle()
	f.shot("settings-full-not-found"+suffix, "Mu couldn't find Pi", "Coming soon", "Edit custom agent")
	f.tt.SetSize(width, height)
	f.settle()
	f.setPi(pi, app.BuiltinReady)
	click(t, f.tt, "Done")

	t.Setenv("FAKEPI_NO_MODELS", "1")
	f.setPi(pi, app.BuiltinSignedOut)
	click(t, f.tt, "New thread")
	f.shot("new-thread-not-signed-in"+suffix, "Pi isn't signed in", "/login")
	click(t, f.tt, "Copy command")
	f.shot("new-thread-copied"+suffix, "Copied")
	click(t, f.tt, "Settings") // Pi is the default, so its details are open.
	f.tt.SetSize(width, 1560)
	f.settle()
	f.shot("settings-full-not-signed-in"+suffix, "Pi isn't signed in", "Sign-in")
	f.tt.SetSize(width, height)
	f.settle()
	os.Unsetenv("FAKEPI_NO_MODELS")
	t.Setenv("FAKEPI_VERSION", "0.9.2")
	f.setPi(pi, app.BuiltinOutdated)
	f.tt.SetSize(width, 1560)
	f.settle()
	f.shot("settings-full-needs-update"+suffix, "Pi needs an update", "Mu needs 1.5.0 or newer")
	f.tt.SetSize(width, height)
	f.settle()
	click(t, f.tt, "Done")
	click(t, f.tt, "New thread")
	f.shot("new-thread-needs-update"+suffix, "Pi needs an update", "pi.dev")
	click(t, f.tt, "Configured agent")
	f.shot("new-thread-agent-menu-needs-update"+suffix, "Needs update")
	f.tt.Key(0, ui.KeyEscape)
	f.settle()
	os.Unsetenv("FAKEPI_VERSION")
	f.setPi(pi, app.BuiltinReady)

	// Settings with Pi ready, and its details out of the way.
	click(t, f.tt, "Settings")
	f.shot("settings-ready"+suffix, "Sign-in", "Tool use", "Projects", "Sessions")
	f.tt.SetSize(width, 1560)
	f.settle()
	f.shot("settings-full-ready"+suffix, "Coming soon", "My custom agent")
	click(t, f.tt, "Agent: Pi")
	f.settle()
	f.shot("settings-full-add-custom-agent"+suffix, "Add a custom agent")
	click(t, f.tt, "Edit agent: My custom agent")
	f.shot("settings-full-edit-custom-agent"+suffix, "Edit custom agent")
	f.tt.SetSize(width, height)
	f.settle()
	click(t, f.tt, "Done")

	// A real thread, through the adapter.
	click(t, f.tt, "New thread")
	click(t, f.tt, "Create thread")
	f.waitThread("the session", idleThread)
	f.shot("thread-new-empty"+suffix, "Send a message to begin.", "Model")
	f.send("hello")
	f.waitThread("a reply", func(v app.ThreadView) bool {
		return idleThread(v) && len(v.Entries) >= 3
	})
	click(t, f.tt, "Model")
	f.shot("thread-model-menu"+suffix, "Deep", "Basic")
	f.tt.Key(0, ui.KeyEscape)
	f.settle()
	click(t, f.tt, "Message")
	f.tt.Type("/")
	f.settle()
	f.shot("thread-slash-commands"+suffix, "/compact", "/fix-tests")
	f.tt.Key(0, ui.KeyEscape)
	f.tt.Key(0, ui.KeyBackspace)
	f.settle()

	// Diffs, for any agent that reports them.
	f.send("edit-file")
	f.waitThread("an edit", func(v app.ThreadView) bool {
		for _, e := range v.Entries {
			if e.Role == "tool" && e.Status == "completed" && len(e.Diffs) > 0 {
				return idleThread(v)
			}
		}
		return false
	})
	f.shot("thread-diff-collapsed"+suffix, "edit a.txt", "+2", "−2")
	click(t, f.tt, "edit a.txt")
	f.shot("thread-diff-expanded"+suffix, "TWO", "EIGHT", "⋯")
	click(t, f.tt, "edit a.txt")
	f.send("write-file")
	f.waitThread("a write", func(v app.ThreadView) bool {
		for _, e := range v.Entries {
			if e.Role == "tool" && e.Title == "write new.txt" && e.Status == "completed" {
				return idleThread(v)
			}
		}
		return false
	})
	click(t, f.tt, "write new.txt")
	f.shot("thread-diff-new-file"+suffix, "fresh", "+1", "−0")
	click(t, f.tt, "write new.txt")

	f.setPi(missing, app.BuiltinNotFound)
	click(t, f.tt, "Import sessions")
	f.shot("import-not-found"+suffix, "My custom agent")
	f.notOffered("import")
	f.setPi(pi, app.BuiltinReady)

	// Import lists what Pi has.
	if text := f.tt.Texts(); !strings.Contains(strings.Join(text, " "), "Pi") {
		t.Fatalf("Pi isn't offered again: %q", text)
	}
	click(t, f.tt, "Discovery agent")
	click(t, f.tt, "Pi")
	click(t, f.tt, "Find sessions")
	deadline := time.Now().Add(20 * time.Second)
	for !f.a.Snapshot().Catalog.Complete {
		if time.Now().After(deadline) {
			t.Fatalf("discovery: %+v", f.a.Snapshot().Catalog)
		}
		f.tt.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	f.settle()
	f.shot("import-found"+suffix, "hello")
}
