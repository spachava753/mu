package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/state"
)

func TestTranscriptDisclosureAnimates(t *testing.T) {
	v := &View{expanded: map[string]*bool{}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			v.entry(c, "thread", state.Entry{ID: "thought", Role: "thought", Text: "First line\nSecond line\nThird line\nFourth line"})
			ui.Text(c, "After thinking")
		})
	}, 600, 400)
	y := func() float32 { r, _ := tt.Find("After thinking"); return r.Y }
	closed := y()
	if err := tt.Click("Thinking"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(60 * time.Millisecond)
	middle := y()
	tt.Advance(250 * time.Millisecond)
	opened := y()
	if middle <= closed || middle >= opened {
		t.Fatalf("disclosure skipped: closed=%v middle=%v open=%v", closed, middle, opened)
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("Thinking"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if y() != closed {
		t.Fatal("disclosure ignored reduced motion")
	}
}
