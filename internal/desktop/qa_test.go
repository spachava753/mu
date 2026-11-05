package desktop_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/testutil"
)

// TestQAConversation drives the real widgets and stdio transport together. The
// fresh local fixture is deterministic and never invokes an external provider.
func TestQAConversation(t *testing.T) {
	a, err := app.Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	view := desktop.New(a, fmt.Sprintf("%q", testutil.Agent(t)))
	tt := ui.NewTester(view.Draw, 1100, 780)
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("click %q: %v", label, err)
		}
		tt.Frame()
		tt.Advance(320 * time.Millisecond)
	}
	await := func(condition func(app.ThreadView) bool) app.ThreadView {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for {
			tt.Frame()
			threads := a.Snapshot().Threads
			if len(threads) != 1 {
				t.Fatalf("expected one thread, got %d", len(threads))
			}
			thread := threads[0]
			if thread.Error != "" {
				t.Fatal(thread.Error)
			}
			if condition(thread) {
				// Render the state that may have arrived after the previous frame.
				tt.Frame()
				return thread
			}
			if time.Now().After(deadline) {
				t.Fatalf("UI timeout: running=%v status=%q entries=%#v", thread.Running, thread.Status, thread.Entries)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	lastText := func(thread app.ThreadView) string {
		if len(thread.Entries) == 0 {
			return ""
		}
		return thread.Entries[len(thread.Entries)-1].Text
	}
	typeMessage := func(text string, enter bool) {
		t.Helper()
		click("Message")
		tt.Type(text)
		tt.Frame()
		if enter {
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
		} else {
			click("Send")
		}
	}
	click("Create thread")
	typeMessage("hello from UI", false)
	await(func(thread app.ThreadView) bool { return !thread.Running && lastText(thread) == "Echo: hello from UI" })
	if !tt.HasText("Echo: hello from UI") {
		t.Fatal("stdio assistant response is not rendered")
	}

	// This also verifies that Send cleared the previous draft.
	typeMessage("permission", true)
	await(func(thread app.ThreadView) bool { return len(thread.Permissions) == 1 })
	if !tt.HasText("Permission needed for Run test command") {
		t.Fatal("permission request is not visible")
	}
	screenshot(t, tt, "permission-1100")
	click("Reject")
	await(func(thread app.ThreadView) bool {
		return !thread.Running && strings.Contains(lastText(thread), `"optionId":"no"`)
	})
	if _, exists := tt.Find("Reject"); exists {
		t.Fatal("answered permission still displayed")
	}

	typeMessage("wait", false)
	await(func(thread app.ThreadView) bool { return thread.Running && lastText(thread) == "Waiting" })
	screenshot(t, tt, "running-1100")
	click("Stop")
	await(func(thread app.ThreadView) bool {
		return !thread.Running && strings.HasPrefix(lastText(thread), "Stopped.")
	})
	if !tt.HasText("Stopped. The response may be incomplete.") {
		t.Fatal("stop notice is not rendered")
	}

	// A new turn must not inherit the prior draft or a delayed cancellation.
	typeMessage("after stop", true)
	await(func(thread app.ThreadView) bool { return !thread.Running && lastText(thread) == "Echo: after stop" })
	if !tt.HasText("Echo: after stop") {
		t.Fatal("composer did not recover after Stop")
	}

	// Stop shows the turn's unfinished tool calls as cancelled.
	typeMessage("tool-wait", true)
	await(func(thread app.ThreadView) bool { return thread.Running && lastText(thread) == "Waiting" })
	click("Stop")
	await(func(thread app.ThreadView) bool { return !thread.Running })
	if _, ok := tt.Find("cancelled"); !ok {
		t.Fatal("stopped tool call is not shown as cancelled")
	}
	screenshot(t, tt, "stopped-tools-1100")
}

// This exercises actual headless widget hit testing, never desktop input.
func TestQAThreadControls(t *testing.T) {
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a, err := app.Open(t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := a.Close(); err != nil {
					t.Error(err)
				}
			})
			view := desktop.New(a, testutil.Agent(t))
			tt := ui.NewTester(view.Draw, size[0], size[1])
			tt.Frame()
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatalf("click %q: %v", label, err)
				}
				tt.Frame()
				tt.Advance(320 * time.Millisecond)
			}
			click("Create thread")
			if len(a.Snapshot().Threads) != 1 {
				t.Fatal("create button did not create thread")
			}
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
			if !tt.HasText("Send a message to begin.") {
				t.Fatal("empty-thread composer not visible")
			}
			click("Fork")
			waitView(t, a, tt, func(s app.Snapshot) bool {
				for _, thread := range s.Threads {
					if thread.ParentID != "" && thread.Connected && !thread.Configuring {
						return true
					}
				}
				return false
			})
			if len(a.Snapshot().Threads) != 2 {
				t.Fatal("fork button did not create thread")
			}
			click("Archive")
			waitView(t, a, tt, func(s app.Snapshot) bool {
				for _, thread := range s.Threads {
					if thread.Configuring {
						return false
					}
				}
				return true
			})
			if !tt.HasText("Archived. Restore this thread to continue.") {
				t.Fatal("archive left composer enabled")
			}
			click("Restore")
			waitView(t, a, tt, func(s app.Snapshot) bool {
				for _, thread := range s.Threads {
					if thread.Configuring {
						return false
					}
				}
				return true
			})
			if _, ok := tt.Find("Message"); !ok {
				t.Fatal("restore did not restore composer")
			}
			removeThread := func() {
				t.Helper()
				click("More thread actions")
				if err := tt.ChooseMenuItem("Remove from Mu…"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				tt.Advance(320 * time.Millisecond)
			}
			removeThread()
			if !tt.HasText("Remove this thread from Mu?") {
				t.Fatal("delete confirmation did not appear")
			}
			click("Keep thread")
			if len(a.Snapshot().Threads) != 2 {
				t.Fatal("cancelled deletion removed thread")
			}
			removeThread()
			click("Remove reference")
			if len(a.Snapshot().Threads) != 1 {
				t.Fatal("confirmed deletion did not remove fork")
			}
			if !tt.HasText("No thread selected") {
				t.Fatal("deleted thread still selected")
			}
		})
	}
}

// Permission cards describe the tool call from Mu's projection, follow arrival
// order, and appear outside a running turn.
func TestQAPermissionCards(t *testing.T) {
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a := testApp(t)
			view := desktop.New(a, testutil.Agent(t))
			tt := ui.NewTester(view.Draw, size[0], size[1])
			tt.Frame()
			click(t, tt, "Create thread")
			send := func(text string) {
				t.Helper()
				click(t, tt, "Message")
				tt.Type(text)
				tt.Key(0, ui.KeyEnter)
				tt.Frame()
			}
			pending := func(n int) func(app.Snapshot) bool {
				return func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == n }
			}
			idle := func(s app.Snapshot) bool { return !s.Threads[0].Running && len(s.Threads[0].Permissions) == 0 }

			// The request names only a known tool call.
			send("permission-ref")
			waitView(t, a, tt, pending(1))
			for _, text := range []string{"Permission needed for Edit notes", "Edit", "Always allow edits", "Tool / protocol details"} {
				if _, ok := tt.Find(text); !ok {
					t.Fatalf("permission card lacks %q: %v", text, tt.Texts())
				}
			}
			for _, text := range []string{"/workspace/notes.txt:3", "old", "new", "+1", "−1"} {
				if !slices.Contains(tt.Texts(), text) {
					t.Fatalf("permission card lacks the tool call's location and diff: %q in %q", text, tt.Texts())
				}
			}
			screenshot(t, tt, fmt.Sprintf("permission-tool-%d", size[0]))
			// Expanded details stay bounded: the composer remains in view, and
			// clicking the option (not merely finding it) answers the request.
			click(t, tt, "Tool / protocol details")
			for _, label := range []string{"Message", "Stop"} {
				r, ok := tt.Find(label)
				if !ok || r.Y < 0 || r.Y+r.H > float32(size[1]) {
					t.Fatalf("%q is out of view with details expanded: %v %v", label, r, ok)
				}
			}
			screenshot(t, tt, fmt.Sprintf("permission-details-%d", size[0]))
			click(t, tt, "Always allow edits")
			waitView(t, a, tt, idle)
			if entries := a.Snapshot().Threads[0].Entries; !strings.Contains(entries[len(entries)-1].Text, `"optionId":"always"`) {
				t.Fatal("the visible option did not answer the request")
			}

			send("permissions-two")
			waitView(t, a, tt, pending(2))
			first, ok1 := tt.Find("Permission needed for First")
			second, ok2 := tt.Find("Permission needed for Second")
			if !ok1 || !ok2 || first.Y >= second.Y {
				t.Fatalf("permission cards out of arrival order: %v %v", first, second)
			}
			screenshot(t, tt, fmt.Sprintf("permissions-two-%d", size[0]))
			click(t, tt, "Stop")
			waitView(t, a, tt, idle)

			send("permission-idle")
			waitView(t, a, tt, func(s app.Snapshot) bool { return !s.Threads[0].Running && len(s.Threads[0].Permissions) == 1 })
			_, kind := tt.Find("Execute")
			if !tt.HasText("Permission needed for Background task") || !kind {
				t.Fatal("idle permission card is not visible", tt.Texts())
			}
			screenshot(t, tt, fmt.Sprintf("permission-idle-%d", size[0]))
			click(t, tt, "Reject")
			waitView(t, a, tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 0 })
		})
	}
}
