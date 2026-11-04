package desktop_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func composerFixture(t *testing.T, width, height int) (*app.App, *ui.Tester) {
	t.Helper()
	a := testApp(t)
	_, err := a.Create(fmt.Sprintf("%q session-ui long-menus", testutil.Agent(t)), "")
	if err != nil {
		t.Fatal(err)
	}
	v := desktop.New(a, "")
	tt := ui.NewTester(v.Draw, width, height)
	tt.SetDark(true)
	return a, tt
}

func TestLongModelOptionsScroll(t *testing.T) {
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a, tt := composerFixture(t, size[0], size[1])
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
			if tt.HasText("Reasoning: low") || !tt.HasText("low") {
				t.Fatal("redundant option prefix was not removed")
			}
			click(t, tt, "Model")
			menu, ok := tt.Find("Model choices")
			if !ok {
				t.Fatal("missing scroll area")
			}
			if menu.H > 301 || menu.Y < 0 || menu.Y+menu.H > float32(size[1]) {
				t.Fatalf("menu exceeds window: %+v", menu)
			}
			// Wheel scrolling must reach the last choice, not scroll anything behind it.
			tt.Move(menu.X+menu.W/2, menu.Y+menu.H/2)
			tt.Frame()
			tt.Scroll(menu.X+menu.W/2, menu.Y+menu.H/2, 0, 10000)
			tt.Frame()
			last, ok := tt.Find("Model 99")
			if !ok || last.Y < menu.Y || last.Y+last.H > menu.Y+menu.H+1 {
				t.Fatalf("last option not reachable: %+v in %+v", last, menu)
			}
			screenshot(t, tt, fmt.Sprintf("scrollable-models-%d", size[0]))
			click(t, tt, "Model 99")
			waitView(t, a, tt, func(s app.Snapshot) bool {
				return !s.Threads[0].Configuring && s.Threads[0].Session.Options[0].CurrentValue == "model-99"
			})
			click(t, tt, "Model")
			tt.Key(0, ui.KeyHome)
			tt.Frame()
			first, _ := tt.Find("Fast")
			menu, _ = tt.Find("Model choices")
			if first.Y < menu.Y || first.Y+first.H > menu.Y+menu.H {
				t.Fatal("keyboard highlight did not scroll into view")
			}
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
			waitView(t, a, tt, func(s app.Snapshot) bool {
				return !s.Threads[0].Configuring && s.Threads[0].Session.Options[0].CurrentValue == "fast"
			})
			usage, ok := tt.Find("Context usage")
			if !ok {
				t.Fatal("no persistent usage indicator")
			}
			message, _ := tt.Find("Message")
			if usage.Y < message.Y+message.H || usage.Y+usage.H > float32(size[1]) || usage.H < 18 {
				t.Fatalf("usage is not visible below the draft: %+v", usage)
			}
			if !tt.HasText("Context 16,000 / 128,000 (12.5%)") {
				t.Fatal("usage label missing outside settings")
			}
			screenshot(t, tt, fmt.Sprintf("composer-usage-%d", size[0]))
			click(t, tt, "Context usage")
			if !tt.HasText("Token usage") {
				t.Fatal("indicator does not expose token details")
			}
			screenshot(t, tt, fmt.Sprintf("usage-details-%d", size[0]))
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			// A long draft grows the composer up to its limit, then scrolls,
			// keeping the toolbar and usage line in the window.
			click(t, tt, "Message")
			for i := range 12 {
				if i > 0 {
					tt.Key(ui.Shift, ui.KeyEnter)
				}
				tt.Type(fmt.Sprintf("Draft line %d with enough words to show how the composer wraps and grows", i+1))
			}
			tt.Frame()
			tt.Advance(320 * time.Millisecond)
			if len(a.Snapshot().Threads[0].Entries) != 0 {
				t.Fatal("Shift+Enter sent the draft")
			}
			message, _ = tt.Find("Message")
			usage, _ = tt.Find("Context usage")
			send, _ := tt.Find("Send")
			if message.H < 100 || send.Y < message.Y+message.H || usage.Y+usage.H > float32(size[1]) {
				t.Fatalf("long draft layout: message %+v send %+v usage %+v", message, send, usage)
			}
			screenshot(t, tt, fmt.Sprintf("composer-multiline-%d", size[0]))
		})
	}
}

func TestSlashCommandPicker(t *testing.T) {
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a, tt := composerFixture(t, size[0], size[1])
			click(t, tt, "Message")
			tt.Type("/")
			tt.Frame()
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
			if len(a.Snapshot().Threads[0].Entries) != 0 {
				t.Fatal("slash discovery sent a prompt")
			}
			popup, ok := tt.Find("Slash commands")
			if !ok {
				t.Fatal("typing slash did not open suggestions", tt.Texts())
			}
			input, _ := tt.Find("Message")
			usage, _ := tt.Find("Context usage")
			if usage.H == 0 || usage.Y < popup.Y+popup.H {
				t.Fatal("suggestions obscure the context indicator")
			}
			if popup.Y < 0 || popup.Y+popup.H > input.Y {
				t.Fatalf("popup not above composer: %+v / %+v", popup, input)
			}
			screenshot(t, tt, fmt.Sprintf("slash-commands-%d", size[0]))
			tt.Type("rev")
			tt.Frame()
			if _, ok := tt.Find("/command00"); ok {
				t.Fatal("commands not filtered")
			}
			if _, ok := tt.Find("/review"); !ok {
				t.Fatal("matching command missing")
			}
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
			if len(a.Snapshot().Threads[0].Entries) != 0 {
				t.Fatal("completion submitted instead of inserting")
			}
			if _, ok := tt.Find("Slash commands"); ok {
				t.Fatal("completion left popup open")
			}
			tt.Type("changes")
			tt.Key(0, ui.KeyEnter)
			waitEcho(t, a, tt, "Echo: /review changes")

			click(t, tt, "Message")
			tt.Type("/")
			tt.Frame()
			tt.Key(0, ui.KeyDown)
			tt.Key(0, ui.KeyTab)
			tt.Frame()
			tt.Type("argument")
			tt.Key(0, ui.KeyEnter)
			waitEcho(t, a, tt, "Echo: /command00 argument")

			click(t, tt, "Message")
			tt.Type("/rev")
			tt.Frame()
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			tt.Frame()
			if _, ok := tt.Find("Slash commands"); ok {
				t.Fatal("Escape did not dismiss suggestions")
			}
			// Editing reopens suggestions; a click inserts and returns the caret.
			tt.Type("i")
			tt.Frame()
			click(t, tt, "/review")
			tt.Frame()
			if !tt.Focused("Message") {
				t.Fatal("mouse completion lost composer focus")
			}
			tt.Type("clicked")
			tt.Key(0, ui.KeyEnter)
			waitEcho(t, a, tt, "Echo: /review clicked")

			click(t, tt, "Message")
			tt.Type("/")
			tt.Frame()
			tt.Key(0, ui.KeyUp)
			tt.Frame()
			menu, _ := tt.Find("Command choices")
			last, _ := tt.Find("/command79")
			if last.H == 0 || last.Y < menu.Y || last.Y+last.H > menu.Y+menu.H+1 {
				t.Fatal("keyboard command selection is outside the scroll area")
			}
			tt.Key(0, ui.KeyTab)
			tt.Frame()
			tt.Type("last")
			tt.Key(0, ui.KeyEnter)
			waitEcho(t, a, tt, "Echo: /command79 last")
			click(t, tt, "Message")
			tt.Type("/")
			tt.Frame()
			menu, _ = tt.Find("Command choices")
			tt.Scroll(menu.X+menu.W/2, menu.Y+menu.H/2, 0, 10000)
			tt.Frame()
			click(t, tt, "/command79")
			tt.Frame()
			tt.Key(0, ui.KeyEnter)
			waitEcho(t, a, tt, "Echo: /command79 ")
			click(t, tt, "Message")
			tt.Type("/where is the slash popup?")
			tt.Frame()
			if _, ok := tt.Find("Slash commands"); ok {
				t.Fatal("command arguments or prose reopened completion")
			}
		})
	}
}

func waitEcho(t *testing.T, a *app.App, tt *ui.Tester, text string) {
	t.Helper()
	waitView(t, a, tt, func(s app.Snapshot) bool {
		v := s.Threads[0]
		return !v.Running && len(v.Entries) > 0 && v.Entries[len(v.Entries)-1].Text == text
	})
}

// Opening a thread connects it, so that its options are there before the first
// message, and no prompt goes to the agent. Archived threads stay disconnected.
func TestOpeningThreadConnects(t *testing.T) {
	a := testApp(t)
	first, err := a.Create(fmt.Sprintf("%q session-ui", testutil.Agent(t)), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Rename(first, "First"); err != nil {
		t.Fatal(err)
	}
	second, err := a.Create(fmt.Sprintf("%q session-ui", testutil.Agent(t)), "")
	if err != nil {
		t.Fatal(err)
	}
	archived, err := a.Create(fmt.Sprintf("%q session-ui", testutil.Agent(t)), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Archive(archived, true); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
	tt.Frame()
	// The newest active thread opens at startup, and only that one connects.
	waitView(t, a, tt, func(s app.Snapshot) bool {
		v := threadView(s, second)
		return v.Connected && !v.Configuring && v.SessionID != ""
	})
	if _, ok := tt.Find("Model"); !ok {
		t.Fatal("options are not there before the first message", tt.Texts())
	}
	if !tt.HasText("Send a message to begin.") {
		t.Fatal("empty thread text", tt.Texts())
	}
	v := threadView(a.Snapshot(), second)
	if len(v.Entries) != 0 || v.Running {
		t.Fatalf("connecting sent a prompt: %+v", v.Entries)
	}
	if v := threadView(a.Snapshot(), first); v.Connected || v.Configuring || v.SessionID != "" {
		t.Fatalf("connected a thread that was not opened: %+v", v.Thread)
	}
	click(t, tt, "Thread: First")
	waitView(t, a, tt, func(s app.Snapshot) bool { return threadView(s, first).Connected })
	if v := threadView(a.Snapshot(), archived); v.Connected || v.SessionID != "" {
		t.Fatalf("connected an archived thread: %+v", v.Thread)
	}
}

// While a new thread's agent starts, the panel, composer and sidebar all say so.
func TestConnectingNewThreadSaysStarting(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create(fmt.Sprintf("%q session-ui slow-init slow-new", testutil.Agent(t)), ""); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
	tt.Frame()
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Configuring })
	got := 0
	for _, text := range tt.Texts() {
		if text == "Starting the agent…" {
			got++
		}
		if strings.Contains(text, "Loading session") || strings.Contains(text, "Updating session") {
			t.Fatalf("a new thread says %q", text)
		}
	}
	if got != 3 {
		t.Fatalf("said it %d times: %q", got, tt.Texts())
	}
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
}

// Pressing Enter while the agent starts sends the message once it is connected.
// The agent stays starting until each test lets it go.
func TestEnterWhileConnectingSendsWhenReady(t *testing.T) {
	fixture := func(t *testing.T, args string) (a *app.App, tt *ui.Tester, release func()) {
		t.Helper()
		a = testApp(t)
		command := testutil.Agent(t)
		store := filepath.Join(filepath.Dir(command), "fixture-sessions")
		release = func() {
			if err := os.MkdirAll(store, 0o700); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(store, "release"), nil, 0o600); err != nil {
				t.Error(err)
			}
		}
		t.Cleanup(release) // Before the app closes.
		if _, err := a.Create(fmt.Sprintf("%q gate %s", command, args), ""); err != nil {
			t.Fatal(err)
		}
		tt = ui.NewTester(desktop.New(a, "").Draw, 800, 600)
		tt.Frame()
		waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Configuring })
		return a, tt, release
	}
	typeAndEnter := func(t *testing.T, tt *ui.Tester) {
		t.Helper()
		click(t, tt, "Message")
		tt.Type("fix the bug")
		tt.Frame()
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
	}
	sent := func(text string) func(app.Snapshot) bool {
		return func(s app.Snapshot) bool {
			th := s.Threads[0]
			return len(th.Entries) > 0 && th.Entries[0].Text == text && !th.Running
		}
	}
	t.Run("sends", func(t *testing.T) {
		a, tt, release := fixture(t, "session-ui")
		typeAndEnter(t, tt)
		if !tt.HasText("Sends when ready…") {
			t.Fatal("no sign the message is waiting", tt.Texts())
		}
		// The keyboard stays in the draft, which cannot change, and a second Enter
		// does not undo the first.
		tt.Type("more")
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if !tt.HasText("Sends when ready…") || !tt.Focused("Message") {
			t.Fatal("a second Enter undid the first", tt.Texts())
		}
		if _, ok := tt.Find("Cancel sending"); !ok {
			t.Fatal("no way to take it back", tt.Texts())
		}
		if th := a.Snapshot().Threads[0]; th.Connected || len(th.Entries) != 0 {
			t.Fatalf("sent too soon: %+v", th.Entries)
		}
		screenshot(t, tt, "send-when-ready")
		release()
		waitView(t, a, tt, sent("fix the bug"))
		if th := a.Snapshot().Threads[0]; th.Title != "fix the bug" {
			t.Fatalf("title %q", th.Title)
		}
		tt.Frame()
		if tt.HasText("Sends when ready…") {
			t.Fatal("still waiting", tt.Texts())
		}
		if !tt.Focused("Message") {
			t.Fatal("the keyboard did not return to the draft")
		}
	})
	t.Run("escape", func(t *testing.T) {
		a, tt, release := fixture(t, "session-ui")
		typeAndEnter(t, tt)
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if tt.HasText("Sends when ready…") {
			t.Fatal("Escape did not take the message back", tt.Texts())
		}
		tt.Type(", please")
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		release()
		waitView(t, a, tt, sent("fix the bug, please"))
	})
	t.Run("cancel", func(t *testing.T) {
		a, tt, release := fixture(t, "session-ui")
		typeAndEnter(t, tt)
		click(t, tt, "Cancel sending")
		release()
		waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
		tt.Frame()
		if th := a.Snapshot().Threads[0]; len(th.Entries) != 0 || th.Title != "New thread" {
			t.Fatalf("sent after cancelling: %q %+v", th.Title, th.Entries)
		}
		// The draft is still there to send.
		tt.Key(0, ui.KeyEnter)
		waitView(t, a, tt, sent("fix the bug"))
	})
	t.Run("connecting fails", func(t *testing.T) {
		a, tt, release := fixture(t, "no-fork")
		typeAndEnter(t, tt)
		release()
		waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].SessionError != "" && !s.Threads[0].Configuring })
		tt.Frame()
		if th := a.Snapshot().Threads[0]; len(th.Entries) != 0 || th.Running {
			t.Fatalf("sent without a session: %+v", th.Entries)
		}
		if tt.HasText("Sends when ready…") {
			t.Fatal("still waiting", tt.Texts())
		}
		screenshot(t, tt, "send-when-ready-failed")
	})
	t.Run("only while connecting", func(t *testing.T) {
		a, tt, release := fixture(t, "session-ui wait-config")
		release()
		waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
		click(t, tt, "Optional behavior")
		click(t, tt, "On")
		waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Configuring })
		click(t, tt, "Message")
		tt.Type("hello")
		tt.Frame()
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if tt.HasText("Sends when ready…") {
			t.Fatal("queued behind an option change, which the agent could refuse")
		}
		if th := a.Snapshot().Threads[0]; len(th.Entries) != 0 {
			t.Fatalf("sent during an option change: %+v", th.Entries)
		}
	})
	t.Run("empty draft", func(t *testing.T) {
		_, tt, _ := fixture(t, "session-ui")
		click(t, tt, "Message")
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if tt.HasText("Sends when ready…") {
			t.Fatal("queued nothing")
		}
	})
}

// A new thread whose agent cannot start says so and offers to try again, since no
// session exists to go back to. Its usage is reported as unknown, not as zero.
func TestFailedConnectionOffersRetry(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create("mu-test-no-such-agent", ""); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
	tt.Frame()
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].SessionError != "" && !s.Threads[0].Configuring })
	first := a.Snapshot().Threads[0].SessionError
	if !tt.HasText("Context not reported") {
		t.Fatal("missing explicit unknown usage state", tt.Texts())
	}
	if !tt.HasText("Send a message to start the agent.") {
		t.Fatal("empty thread text", tt.Texts())
	}
	screenshot(t, tt, "connection-failed")
	click(t, tt, "Retry connection")
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].SessionError != "" && !s.Threads[0].Configuring })
	if got := a.Snapshot().Threads[0].SessionError; got != first {
		t.Fatalf("retry reported %q, then %q", first, got)
	}
}

// Every advertised option is a menu in the composer toolbar, whatever its type,
// and there is no second place to change them.
func TestComposerOffersEveryOption(t *testing.T) {
	a, tt := composerFixture(t, 800, 600)
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	click(t, tt, "More thread actions")
	if tt.ChooseMenuItem("Session options…") == nil {
		t.Fatal("the options dialog is still offered")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	option := func(name string) any {
		for _, o := range a.Snapshot().Threads[0].Session.Options {
			if string(o.ID) == name || o.Name == name {
				return o.CurrentValue
			}
		}
		t.Fatal("no option", name)
		return nil
	}
	if option("enabled") != false {
		t.Fatal("fixture boolean option", option("enabled"))
	}
	click(t, tt, "Optional behavior")
	click(t, tt, "On")
	waitView(t, a, tt, func(s app.Snapshot) bool { return !s.Threads[0].Configuring && option("enabled") == true })
	screenshot(t, tt, "composer-options")
}

// Options beyond what the composer can hold scroll, and the draft and its
// actions stay in the window.
func TestManyOptionsStayReachable(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create(fmt.Sprintf("%q session-ui many-options", testutil.Agent(t)), ""); err != nil {
		t.Fatal(err)
	}
	const height = 600
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, height)
	tt.Frame()
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	for _, label := range []string{"Message", "Send", "Context usage"} {
		if r, ok := tt.Find(label); !ok || r.Y < 0 || r.Y+r.H > height {
			t.Fatalf("%s left the window: %+v", label, r)
		}
	}
	area, ok := tt.Find("Session options")
	if !ok || area.H > 100 {
		t.Fatalf("options are not bounded: %+v", area)
	}
	last, ok := tt.Find("Optional behavior")
	if !ok || last.Y < area.Y+area.H {
		t.Fatalf("expected the last option below the fold: %+v in %+v", last, area)
	}
	tt.Scroll(area.X+area.W/2, area.Y+area.H/2, 0, 10000)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if last, _ = tt.Find("Optional behavior"); last.Y < area.Y || last.Y+last.H > area.Y+area.H+1 {
		t.Fatalf("the last option is out of reach: %+v in %+v", last, area)
	}
	screenshot(t, tt, "composer-many-options")
	click(t, tt, "Optional behavior")
	click(t, tt, "On")
	waitView(t, a, tt, func(s app.Snapshot) bool {
		options := s.Threads[0].Session.Options
		return !s.Threads[0].Configuring && options[len(options)-1].CurrentValue == true
	})
}

// Permissions yield room to a full composer, so its controls stay in the window,
// but a long conversation does not squeeze them away.
func TestComposerKeepsRoomWithPermissions(t *testing.T) {
	a := testApp(t)
	id, err := a.Create(fmt.Sprintf("%q session-ui many-options", testutil.Agent(t)), "")
	if err != nil {
		t.Fatal(err)
	}
	const height = 600
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, height)
	tt.Frame()
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	send := func(prompt string) {
		t.Helper()
		if err := a.Send(id, prompt); err != nil {
			t.Fatal(err)
		}
		waitView(t, a, tt, func(s app.Snapshot) bool { return !s.Threads[0].Running && !s.Threads[0].Configuring })
	}
	for range 3 {
		send("updates")
	}
	send("permission-idle")
	waitView(t, a, tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 1 })
	area, ok := tt.Find("Permission requests")
	if !ok || area.H < 100 {
		t.Fatalf("a long conversation squeezed the permission: %+v", area)
	}
	send("permission-idle")
	waitView(t, a, tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 2 })
	click(t, tt, "Tool / protocol details")
	click(t, tt, "Message")
	tt.Type(strings.Repeat("a long draft line\n", 12))
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	for _, label := range []string{"Message", "Send", "Context usage"} {
		if r, ok := tt.Find(label); !ok || r.Y < 0 || r.Y+r.H > height {
			t.Fatalf("%s left the window: %+v", label, r)
		}
	}
	screenshot(t, tt, "composer-full-with-permissions")
}

// Below the composer: the thread's branch, its worktree folder if it is a linked
// one, and nothing outside a repository.
func TestGitLine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a := testApp(t)
			id, err := a.Create(testutil.Agent(t), "")
			if err != nil {
				t.Fatal(err)
			}
			dir := a.Snapshot().Threads[0].Cwd
			tt := ui.NewTester(desktop.New(a, "").Draw, size[0], size[1])
			tt.Frame()
			if _, ok := tt.Find("Git branch: feature/resize"); ok {
				t.Fatal("git line outside a repository")
			}
			git(dir, "init", "-q", "-b", "feature/resize")
			git(dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "one")
			if err := a.Send(id, "hello"); err != nil { // The end of the turn reads the folder.
				t.Fatal(err)
			}
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Git.Branch == "feature/resize" && !s.Threads[0].Running })
			line, ok := tt.Find("Git branch: feature/resize")
			if !ok {
				t.Fatal("no git line", tt.Texts())
			}
			usage, _ := tt.Find("Context usage")
			if line.Y < usage.Y+usage.H-1 || line.Y+line.H > float32(size[1]) {
				t.Fatalf("git line %+v is not below usage %+v in the window", line, usage)
			}
			screenshot(t, tt, fmt.Sprintf("git-line-%d", size[0]))

			// A linked worktree also shows its folder, in a project of its own.
			linked := filepath.Join(t.TempDir(), "mu-resize")
			git(dir, "worktree", "add", "-q", "-b", "side", linked)
			project, err := a.SaveProject(state.Project{Name: "Linked", Dir: linked})
			if err != nil {
				t.Fatal(err)
			}
			second, err := a.Create(testutil.Agent(t), project)
			if err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			click(t, tt, "Thread: "+threadTitle(t, a, second))
			waitView(t, a, tt, func(s app.Snapshot) bool { return threadView(s, second).Git.Worktree != "" })
			if _, ok := tt.Find("Worktree: " + linked); !ok || !tt.HasText("mu-resize") || !tt.HasText("side") {
				t.Fatal("no worktree folder", tt.Texts())
			}
			screenshot(t, tt, fmt.Sprintf("git-line-worktree-%d", size[0]))
		})
	}
}

// The git line also shows the branch's pull request, from a stand-in gh.
func TestPullRequestLine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, tt := range []struct {
		name, reply, want string
	}{
		{"open", `{"number":1234,"state":"OPEN","isDraft":false,"title":"Keep it"}`, "PR #1234 · Open"},
		{"draft", `{"number":12,"state":"OPEN","isDraft":true,"title":"Not yet"}`, "PR #12 · Draft"},
		{"merged", `{"number":13,"state":"MERGED","isDraft":false,"title":"Done"}`, "PR #13 · Merged"},
		{"closed", `{"number":14,"state":"CLOSED","isDraft":false,"title":"Dropped"}`, "PR #14 · Closed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testutil.FakeGH(t, "echo '"+tt.reply+"'")
			a := testApp(t)
			if _, err := a.Create("unused-agent", ""); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("git", "init", "-q", "-b", "feature/resize")
			cmd.Dir = a.Snapshot().Threads[0].Cwd
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			tt2 := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
			tt2.Frame()
			waitView(t, a, tt2, func(s app.Snapshot) bool { return s.Threads[0].PullRequest.Found() })
			if !tt2.HasText(tt.want) || !tt2.HasText("feature/resize") {
				t.Fatal("no pull request on the line", tt2.Texts())
			}
			screenshot(t, tt2, "pull-request-"+tt.name)
		})
	}
}

// The pull request opens in the browser, if it has an address to open.
func TestPullRequestOpens(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	const label = "PR #12 · Open: Fix it"
	open := func(t *testing.T, url string) (*app.App, *ui.Tester) {
		t.Helper()
		testutil.FakeGH(t, `echo '{"number":12,"state":"OPEN","isDraft":false,"title":"Fix it","url":"`+url+`"}'`)
		a := testApp(t)
		if _, err := a.Create("unused-agent", ""); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "init", "-q", "-b", "main")
		cmd.Dir = a.Snapshot().Threads[0].Cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
		tt.Frame()
		waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].PullRequest.Found() })
		return a, tt
	}
	t.Run("opens", func(t *testing.T) {
		const url = "https://github.com/o/r/pull/12"
		_, tt := open(t, url)
		click(t, tt, label)
		if got := tt.OpenedURLs(); len(got) != 1 || got[0] != url {
			t.Fatalf("opened %q", got)
		}
		r, ok := tt.Find(label)
		if !ok {
			t.Fatal("no pull request")
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
		if tt.Cursor() != ui.CursorPointer {
			t.Fatal("the pull request does not look clickable")
		}
		tt.Advance(160 * time.Millisecond)
		screenshot(t, tt, "pull-request-hover")
	})
	t.Run("nothing to open it", func(t *testing.T) {
		_, tt := open(t, "https://github.com/o/r/pull/12")
		tt.FailOpenURL(errors.New("no browser"))
		click(t, tt, label)
		if !tt.HasText("no browser") {
			t.Fatal("the failure is not shown", tt.Texts())
		}
	})
	t.Run("not a web address", func(t *testing.T) {
		_, tt := open(t, "file:///etc/passwd")
		click(t, tt, label)
		if got := tt.OpenedURLs(); len(got) != 0 {
			t.Fatalf("opened %q", got)
		}
	})
}

func threadView(s app.Snapshot, id string) app.ThreadView {
	for _, v := range s.Threads {
		if v.ID == id {
			return v
		}
	}
	return app.ThreadView{}
}

func threadTitle(t *testing.T, a *app.App, id string) string {
	t.Helper()
	return threadView(a.Snapshot(), id).Title
}
