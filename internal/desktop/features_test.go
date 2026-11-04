package desktop_test

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func testApp(t *testing.T) *app.App {
	t.Helper()
	a, err := app.Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}
func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(320 * time.Millisecond)
}
func waitView(t *testing.T, a *app.App, tt *ui.Tester, condition func(app.Snapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		tt.Frame()
		if condition(a.Snapshot()) {
			// A worker may complete after the prior frame captured its snapshot.
			tt.Frame()
			tt.Advance(320 * time.Millisecond)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("UI timed out: %+v", a.Snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func screenshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	tt.Advance(320 * time.Millisecond)
	dir := os.Getenv("MU_QA_RENDER_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestMatteSidebarAndSessionControls(t *testing.T) {
	command := fmt.Sprintf("%q session-ui", testutil.Agent(t))
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a := testApp(t)
			agentID, err := a.SaveAgent(state.AgentConfig{Name: "Workbench", Command: command}, true)
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
			view := desktop.New(a, "")
			tt := ui.NewTester(view.Draw, size[0], size[1])
			tt.SetDark(true)
			if err := a.Send(first, "first thread"); err != nil {
				t.Fatal(err)
			}
			if err := a.Send(second, "second thread"); err != nil {
				t.Fatal(err)
			}
			waitView(t, a, tt, func(s app.Snapshot) bool { return !s.Threads[0].Running && !s.Threads[1].Running })
			// Use labels on the complete buttons, not text bounds, to verify surfaces.
			r1, ok := tt.Find("Thread: first thread")
			if !ok {
				t.Fatal("missing first row")
			}
			r2, ok := tt.Find("Thread: second thread")
			if !ok {
				t.Fatal("missing second row")
			}
			pixel := func(r ui.Rect) any { return tt.Image().At(int(r.X+r.W-12), int(r.Y+6)) }
			base, selected := pixel(r1), pixel(r2)
			if base == selected {
				t.Fatal("selected thread has no distinct surface")
			}
			tt.Move(r1.X+r1.W/2, r1.Y+r1.H/2)
			tt.Frame()
			tt.Advance(160 * time.Millisecond)
			if pixel(r1) == base {
				t.Fatal("hover did not highlight the row")
			}
			if pixel(r2) != selected {
				t.Fatal("hover removed the active highlight")
			}
			click(t, tt, "Thread: first thread")
			click(t, tt, "Collapse sidebar")
			if !a.Snapshot().SidebarCollapsed {
				t.Fatal("collapse not saved")
			}
			if _, ok := tt.Find("Thread: first thread"); ok {
				t.Fatal("thread list still visible when collapsed")
			}
			if _, ok := tt.Find("Expand sidebar"); !ok {
				t.Fatal("no way to restore sidebar")
			}
			click(t, tt, "Expand sidebar")
			click(t, tt, "Session options")
			waitView(t, a, tt, func(s app.Snapshot) bool {
				for _, v := range s.Threads {
					if v.ID == first {
						return v.Connected && !v.Configuring
					}
				}
				return false
			})
			click(t, tt, "Set Model")
			click(t, tt, "Deep")
			waitView(t, a, tt, func(s app.Snapshot) bool {
				for _, v := range s.Threads {
					if v.ID == first {
						return !v.Configuring && v.Session.Options[0].CurrentValue == "deep"
					}
				}
				return false
			})
			if !tt.HasText("high") {
				t.Fatal("dependent reasoning option did not refresh")
			}
			screenshot(t, tt, fmt.Sprintf("session-options-%d", size[0]))
			click(t, tt, "Close options")
			if err := a.Send(first, "updates"); err != nil {
				t.Fatal(err)
			}
			waitView(t, a, tt, func(s app.Snapshot) bool {
				for _, v := range s.Threads {
					if v.ID == first {
						return !v.Running && v.Session.TurnUsage != nil
					}
				}
				return false
			})
			if !tt.HasText("Context 16,000 / 128,000 (12.5%)") || !tt.HasText("Cost USD 0.2500") {
				t.Fatal("context limit/cost not rendered", tt.Texts())
			}
			if !tt.HasText("Session controls and usage") {
				t.Fatal("session title update not rendered")
			}
			screenshot(t, tt, fmt.Sprintf("matte-dark-%d", size[0]))
			click(t, tt, "Read configuration")
			if !tt.HasText("Configuration loaded.") {
				t.Fatal("tool disclosure did not open")
			}
			click(t, tt, "Tool / protocol details")
			screenshot(t, tt, fmt.Sprintf("tool-expanded-%d", size[0]))
			tt.SetDark(false)
			tt.Frame()
			screenshot(t, tt, fmt.Sprintf("matte-light-%d", size[0]))
			click(t, tt, "Collapse sidebar")
			screenshot(t, tt, fmt.Sprintf("collapsed-%d", size[0]))
		})
	}
}

func TestAgentSettingsUI(t *testing.T) {
	a := testApp(t)
	view := desktop.New(a, "")
	tt := ui.NewTester(view.Draw, 1100, 780)
	click(t, tt, "Settings")
	click(t, tt, "Agent name")
	tt.Type("My configured agent")
	tt.Frame()
	click(t, tt, "Saved agent command")
	tt.Type("my-agent --stdio")
	tt.Frame()
	click(t, tt, "Save agent")
	s := a.Snapshot()
	if len(s.Agents) != 1 || s.DefaultAgentID != s.Agents[0].ID {
		t.Fatal("settings save failed", s)
	}
	screenshot(t, tt, "agent-settings")
	click(t, tt, "Done")
	click(t, tt, "New thread")
	if _, ok := tt.Find("Agent command"); ok {
		t.Fatal("new thread asks for an agent command again")
	}
	if !tt.HasText("My configured agent") {
		t.Fatal("default configuration is not selected")
	}
	click(t, tt, "Create thread")
	s = a.Snapshot()
	if len(s.Threads) != 1 || s.Threads[0].Command != "my-agent --stdio" || s.Threads[0].AgentID != s.DefaultAgentID {
		t.Fatal("wrong agent configured on thread", s)
	}
	click(t, tt, "Settings")
	click(t, tt, "Remove agent")
	click(t, tt, "Keep agent")
	if len(a.Snapshot().Agents) != 1 {
		t.Fatal("cancelled removal deleted agent")
	}
	click(t, tt, "Remove agent")
	click(t, tt, "Remove configuration")
	if len(a.Snapshot().Agents) != 0 || a.Snapshot().Threads[0].Command != "my-agent --stdio" {
		t.Fatal("removal affected existing thread")
	}
}

func TestProjectsUI(t *testing.T) {
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprint(size[0]), func(t *testing.T) { projectsUI(t, size[0], size[1]) })
	}
}

func projectsUI(t *testing.T, width, height int) {
	a := testApp(t)
	folder := t.TempDir()
	view := desktop.New(a, "unused-agent")
	tt := ui.NewTester(view.Draw, width, height)
	if _, err := a.Create("unused-agent", ""); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "Settings")
	click(t, tt, "Add project")
	click(t, tt, "Project name")
	tt.Type("Website")
	tt.Frame()
	click(t, tt, "Project folder")
	tt.Type(folder)
	tt.Frame()
	click(t, tt, "Save project")
	s := a.Snapshot()
	if len(s.Projects) != 1 || s.Projects[0].Dir != folder || !tt.HasText("Project saved.") {
		t.Fatal("project not saved", s.Projects, tt.Texts())
	}
	screenshot(t, tt, fmt.Sprintf("project-settings-%d", width))
	click(t, tt, "Done")
	click(t, tt, "New thread")
	screenshot(t, tt, fmt.Sprintf("new-thread-%d", width))
	if !tt.HasText("No project") || !tt.HasText("Works in a new, randomly named folder in") {
		t.Fatal("new thread does not default to its own folder", tt.Texts())
	}
	click(t, tt, "Thread project")
	click(t, tt, "Website")
	if !tt.HasText("shared by every thread in this project.") {
		t.Fatal("project folder not explained", tt.Texts())
	}
	screenshot(t, tt, fmt.Sprintf("new-thread-project-%d", width))
	click(t, tt, "Create thread")
	s = a.Snapshot()
	if len(s.Threads) != 2 || s.Threads[0].Cwd != folder || s.Threads[0].ProjectID != s.Projects[0].ID {
		t.Fatal("thread not created in the project", s.Threads)
	}
	if !tt.HasText("Website") || !tt.HasText("No project") {
		t.Fatal("sidebar does not group threads by project", tt.Texts())
	}
	screenshot(t, tt, fmt.Sprintf("project-sidebar-%d", width))
	// Starting another thread from a project thread keeps its project.
	click(t, tt, "New thread")
	click(t, tt, "Create thread")
	if s = a.Snapshot(); len(s.Threads) != 3 || s.Threads[0].ProjectID != s.Threads[1].ProjectID {
		t.Fatal("new thread left the current project", s.Threads)
	}
	click(t, tt, "Settings")
	click(t, tt, "Edit project: Website")
	click(t, tt, "Remove project")
	if s = a.Snapshot(); len(s.Projects) != 0 || s.Threads[0].Cwd != folder {
		t.Fatal("project removal changed threads", s)
	}
}

func TestRenameThreadUI(t *testing.T) {
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a := testApp(t)
			if _, err := a.Create(testutil.Agent(t), ""); err != nil {
				t.Fatal(err)
			}
			view := desktop.New(a, "")
			tt := ui.NewTester(view.Draw, size[0], size[1])
			tt.Frame()
			// From the thread's header menu.
			click(t, tt, "More thread actions")
			if err := tt.ChooseMenuItem("Rename…"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			tt.Advance(320 * time.Millisecond)
			if !tt.HasText("Rename thread") {
				t.Fatal("rename dialog did not open")
			}
			screenshot(t, tt, fmt.Sprintf("rename-dialog-%d", size[0]))
			tt.Type("New thread two") // Replaces the selected title.
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
			tt.Advance(320 * time.Millisecond)
			if got := a.Snapshot().Threads[0].Title; got != "New thread two" {
				t.Fatalf("title after Enter: %q", got)
			}
			if _, open := tt.Find("Rename"); open {
				t.Fatal("rename dialog stayed open")
			}
			// From the sidebar row's context menu, with Cancel leaving it alone.
			if err := tt.RightClick("Thread: New thread two"); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem("Rename…"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			tt.Advance(320 * time.Millisecond)
			tt.Type(" ignored")
			tt.Frame()
			click(t, tt, "Cancel")
			if got := a.Snapshot().Threads[0].Title; got != "New thread two" {
				t.Fatalf("cancel changed the title: %q", got)
			}
			// A double click on the header's title opens it too.
			r, ok := tt.Find("Current thread title")
			if !ok {
				t.Fatal("title not visible")
			}
			for range 2 {
				tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
			}
			tt.Frame()
			tt.Advance(320 * time.Millisecond)
			if !tt.HasText("Rename thread") {
				t.Fatal("double click did not open the dialog")
			}
		})
	}
}
