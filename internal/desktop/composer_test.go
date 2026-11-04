package desktop_test

import (
	"fmt"
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
			click(t, tt, "Connect session")
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
			if !tt.HasText("Context not reported") {
				t.Fatal("missing explicit unknown usage state")
			}
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

// Every advertised option is a menu in the composer toolbar, whatever its type,
// and there is no second place to change them.
func TestComposerOffersEveryOption(t *testing.T) {
	a, tt := composerFixture(t, 800, 600)
	if _, ok := tt.Find("Model"); ok {
		t.Fatal("options shown before the session exists")
	}
	click(t, tt, "Connect session")
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	if _, ok := tt.Find("Connect session"); ok {
		t.Fatal("still offers to connect a connected session")
	}
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
	click(t, tt, "Connect session")
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
