package desktop_test

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
)

func TestAgentCommandIsUserConfigured(t *testing.T) {
	for _, tc := range []struct{ name, saved, override, typed, want string }{
		{name: "no built-in default", typed: "custom-agent --acp", want: "custom-agent --acp"},
		{name: "remember user's command", saved: "previous-agent --stdio", want: "previous-agent --stdio"},
		{name: "explicit override wins", saved: "previous-agent --stdio", override: "another-agent --acp", want: "another-agent --acp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := app.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := a.Close(); err != nil {
					t.Error(err)
				}
			})
			cwd := t.TempDir()
			if tc.saved != "" {
				if _, err := a.Create(tc.saved, cwd); err != nil {
					t.Fatal(err)
				}
			}
			view := desktop.New(a, tc.override, cwd)
			tt := ui.NewTester(view.Draw, 1100, 780)
			if err := tt.Click("New thread"); err != nil {
				t.Fatal(err)
			}
			if tc.typed != "" {
				_ = tt.Click("Create thread") // Must remain disabled without a command.
				if len(a.Snapshot().Threads) != 0 {
					t.Fatal("created a thread with an assumed agent")
				}
				for i := 0; i < 8 && !tt.Focused("Agent command"); i++ {
					tt.Key(0, ui.KeyTab)
				}
				if !tt.Focused("Agent command") {
					t.Fatal("agent command field is not keyboard-accessible")
				}
				tt.Type(tc.typed)
				tt.Frame()
			}
			if err := tt.Click("Create thread"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			got := a.Snapshot()
			if got.LastCommand != tc.want {
				t.Fatalf("command = %q, want %q", got.LastCommand, tc.want)
			}
		})
	}
}
