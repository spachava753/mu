package desktop_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func TestSelectiveSessionImportUI(t *testing.T) {
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			command, cwd := testutil.Agent(t), t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p, err := agent.Start(ctx, command, cwd, agent.Hooks{})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			var ids []string
			// More sessions than fit at 800×600, so the list has to scroll.
			for i := 0; i < 12; i++ {
				result, err := p.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, string(result.SessionId))
				if _, err := p.Conn.Prompt(ctx, acp.PromptRequest{SessionId: result.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("history from another client")}}); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: result.SessionId}); err != nil {
					t.Fatal(err)
				}
			}
			p.Close()
			a, err := app.Open(t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := a.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true); err != nil {
				t.Fatal(err)
			}
			view := desktop.New(a, "")
			tt := ui.NewTester(view.Draw, size[0], size[1])
			tt.Frame()
			click(t, tt, "Import sessions")
			click(t, tt, "Find sessions")
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Catalog.Complete && !s.Catalog.Loading })
			if len(a.Snapshot().Threads) != 0 || !tt.HasText("12 sessions not added to Mu") {
				t.Fatal("discovery auto-imported or hid notice", tt.Texts())
			}
			button, ok := tt.Find("Add selected to Mu")
			if !ok || button.Y+button.H > float32(size[1]) {
				t.Fatal("import action is outside the viewport", button)
			}
			screenshot(t, tt, fmt.Sprintf("session-import-%d", size[0]))
			list, ok := tt.Find("Discovered sessions")
			if !ok || list.Y+list.H > button.Y {
				t.Fatalf("session list overlaps its action: %+v / %+v", list, button)
			}
			tt.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 10000)
			tt.Frame()
			var last ui.Rect
			for _, id := range ids {
				if r, ok := tt.Find("Select session: " + id); ok && r.Y > last.Y {
					last = r
				}
			}
			if last.H == 0 || last.Y < list.Y || last.Y+last.H > list.Y+list.H+1 {
				t.Fatalf("last session not reachable: %+v in %+v", last, list)
			}
			screenshot(t, tt, fmt.Sprintf("session-import-scrolled-%d", size[0]))
			click(t, tt, "Add selected to Mu")
			if len(a.Snapshot().Threads) != 0 {
				t.Fatal("imported without selection")
			}
			click(t, tt, "Filter sessions")
			tt.Type(ids[0])
			tt.Frame()
			click(t, tt, "Select session: "+ids[0])
			click(t, tt, "Add selected to Mu")
			s := a.Snapshot()
			if len(s.Threads) != 1 || s.Threads[0].SessionID != ids[0] || len(s.Threads[0].Entries) != 0 {
				t.Fatal("selection ignored or imported history eagerly", s.Threads)
			}
			click(t, tt, "Thread: Fixture session")
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
			if !tt.HasText("Echo: history from another client") {
				t.Fatal("opening reference did not replay history", tt.Texts())
			}
			click(t, tt, "More thread actions")
			if err := tt.ChooseMenuItem("Reload history"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
			if len(a.Snapshot().Threads[0].Entries) != 2 {
				t.Fatal("reload duplicated history")
			}
		})
	}
}

func TestUnsupportedAgentErrorOutsideOptionsDialog(t *testing.T) {
	a, err := app.Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	view := desktop.New(a, testutil.Agent(t)+" no-fork")
	tt := ui.NewTester(view.Draw, 800, 600)
	tt.Frame()
	click(t, tt, "Create thread")
	click(t, tt, "Session options")
	waitView(t, a, tt, func(s app.Snapshot) bool { return !s.Threads[0].Configuring && s.Threads[0].SessionError != "" })
	click(t, tt, "Close options")
	if !strings.Contains(strings.Join(tt.Texts(), " "), "missing required capabilities: session/fork") {
		t.Fatal("unsupported-agent error only visible in modal", tt.Texts())
	}
}
