package desktop_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
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
			click(t, tt, "Session options")
			waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
			if tt.HasText("Reasoning: low") || !tt.HasText("low") {
				t.Fatal("redundant option prefix was not removed")
			}
			click(t, tt, "Set Model")
			menu, ok := tt.Find("Set Model choices")
			if !ok {
				t.Fatal("missing scroll area")
			}
			if menu.H > 301 || menu.Y < 0 || menu.Y+menu.H > float32(size[1]) {
				t.Fatalf("menu exceeds window: %+v", menu)
			}
			// Wheel scrolling must reach the last choice, not scroll the modal.
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
				return !s.Threads[0].Configuring && s.Threads[0].Session.Options[0].Select.CurrentValue == "model-99"
			})
			click(t, tt, "Set Model")
			tt.Key(0, ui.KeyHome)
			tt.Frame()
			first, _ := tt.Find("Fast")
			menu, _ = tt.Find("Set Model choices")
			if first.Y < menu.Y || first.Y+first.H > menu.Y+menu.H {
				t.Fatal("keyboard highlight did not scroll into view")
			}
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
			waitView(t, a, tt, func(s app.Snapshot) bool {
				return !s.Threads[0].Configuring && s.Threads[0].Session.Options[0].Select.CurrentValue == "fast"
			})
			click(t, tt, "Close options")
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
