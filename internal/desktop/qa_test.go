package desktop_test

import (
	"fmt"
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
	a, err := app.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	view := desktop.New(a, fmt.Sprintf("%q", testutil.Agent(t)), t.TempDir())
	tt := ui.NewTester(view.Draw, 1100, 780)
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("click %q: %v", label, err)
		}
		tt.Frame()
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
	click("Reject")
	await(func(thread app.ThreadView) bool {
		return !thread.Running && strings.Contains(lastText(thread), `"optionId":"no"`)
	})
	if _, exists := tt.Find("Reject"); exists {
		t.Fatal("answered permission still displayed")
	}

	typeMessage("wait", false)
	await(func(thread app.ThreadView) bool { return thread.Running && lastText(thread) == "Waiting" })
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
}

// This exercises actual headless widget hit testing, never desktop input.
func TestQAThreadControls(t *testing.T) {
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			a, err := app.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := a.Close(); err != nil {
					t.Error(err)
				}
			})
			view := desktop.New(a, "qa-agent-not-started", t.TempDir())
			tt := ui.NewTester(view.Draw, size[0], size[1])
			tt.Frame()
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatalf("click %q: %v", label, err)
				}
				tt.Frame()
			}
			click("Create thread")
			if len(a.Snapshot().Threads) != 1 {
				t.Fatal("create button did not create thread")
			}
			if !tt.HasText("Send a message to start the agent.") {
				t.Fatal("empty-thread composer not visible")
			}
			click("Fork")
			if len(a.Snapshot().Threads) != 2 {
				t.Fatal("fork button did not create thread")
			}
			if !tt.HasText("Forked with transcript context. Agent-internal state and workspace files are not copied.") {
				t.Fatal("portable fork warning not displayed")
			}
			click("Archive")
			if !tt.HasText("Archived. Restore this thread to continue.") {
				t.Fatal("archive left composer enabled")
			}
			click("Restore")
			if _, ok := tt.Find("Message"); !ok {
				t.Fatal("restore did not restore composer")
			}
			click("Delete")
			if !tt.HasText("Delete this thread?") {
				t.Fatal("delete confirmation did not appear")
			}
			click("Keep thread")
			if len(a.Snapshot().Threads) != 2 {
				t.Fatal("cancelled deletion removed thread")
			}
			click("Delete")
			click("Delete permanently")
			if len(a.Snapshot().Threads) != 1 {
				t.Fatal("confirmed deletion did not remove fork")
			}
			if !tt.HasText("No thread selected") {
				t.Fatal("deleted thread still selected")
			}
		})
	}
}
