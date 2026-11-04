package desktop

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

func TestShimmerSweep(t *testing.T) {
	const n = 8
	peak := func(phase float64) int {
		best, at := float32(-1), -1
		for i := range n {
			if level := shimmerLevel(i, n, phase); level > best {
				best, at = level, i
			}
		}
		return at
	}
	// The band moves left to right across the text.
	if !(peak(0.35) < peak(0.5) && peak(0.5) < peak(0.65)) {
		t.Fatal("the band does not sweep forward", peak(0.35), peak(0.5), peak(0.65))
	}
	// Levels stay in range and the middle of the sweep highlights something.
	for phase := 0.0; phase < 1; phase += 0.01 {
		for i := range n {
			if level := shimmerLevel(i, n, phase); level < 0 || level > 1 {
				t.Fatalf("level %v at phase %v", level, phase)
			}
		}
	}
	if shimmerLevel(4, n, 0.5) < 0.8 {
		t.Fatal("no highlight mid-sweep")
	}
	// Each sweep starts and ends at rest, so the loop has no visible jump.
	for i := range n {
		if shimmerLevel(i, n, 0) != 0 || shimmerLevel(i, n, 0.9999) > 0.01 {
			t.Fatalf("character %d is lit at the ends of a sweep", i)
		}
	}
	// The phase repeats with the period.
	at := time.Unix(100, 0)
	if shimmerPhase(at) != shimmerPhase(at.Add(shimmerPeriod)) {
		t.Fatal("the phase does not repeat")
	}
	if a, b := shimmerPhase(at), shimmerPhase(at.Add(shimmerPeriod/2)); a == b {
		t.Fatal("the phase does not advance")
	}
}

// Repainting the text builds no view, and text out of view asks for nothing.
func TestShimmerRepaintsOnlyInView(t *testing.T) {
	for _, visible := range []bool{true, false} {
		t.Run(fmt.Sprintf("visible=%v", visible), func(t *testing.T) {
			builds, paints := 0, 0
			tt := ui.NewTester(func(c *ui.Context) {
				builds++
				ui.Box(c).Size(10, 10).Draw(func(*ui.Painter, ui.Rect) { paints++ })
				ui.Column(c).Height(40).Clip().Children(func() {
					if !visible {
						ui.Box(c).Height(200).Shrink(0)
					}
					shimmerText(c, "Working…", 12, ui.RGB(120, 120, 120), ui.RGB(255, 255, 255)).Shrink(0)
				})
			}, 300, 100)
			tt.Frame()
			shot := append([]byte(nil), tt.Image().Pix...)
			builds, paints = 0, 0
			moved := false
			for range 10 {
				tt.Advance(100 * time.Millisecond)
				moved = moved || !bytes.Equal(shot, tt.Image().Pix)
			}
			if builds != 0 {
				t.Fatalf("%d view builds for a color change", builds)
			}
			if visible && (paints < 10 || !moved) {
				t.Fatalf("visible text is not repainted: %d paints, moved=%v", paints, moved)
			}
			if !visible && (paints != 0 || moved) {
				t.Fatalf("text out of view keeps repainting: %d paints, moved=%v", paints, moved)
			}
		})
	}
}

// At rest the painted text is exactly what plain text draws: nothing else shows
// through it, and it does not shift when the agent stops.
func TestShimmerAtRestMatchesPlainText(t *testing.T) {
	for _, size := range []float32{11.5, 12} {
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Padding(10).Children(func() {
				shimmerText(c, "Working…gy", size, ui.RGB(90, 120, 200), ui.RGB(255, 255, 255))
			})
		}, 300, 60)
		tt.Frame()
		tt.Advance(shimmerPeriod - time.Duration(tt.Now().UnixNano()%int64(shimmerPeriod)))
		tt.Frame()
		painted := append([]byte(nil), tt.Image().Pix...)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		tt.Frame()
		if !bytes.Equal(painted, tt.Image().Pix) {
			t.Fatalf("at size %v the painted text differs from plain text", size)
		}
	}
}

func TestAgentWorking(t *testing.T) {
	running := app.ThreadView{Conversation: app.Conversation{Running: true}}
	if !agentWorking(running) {
		t.Fatal("a running turn is work")
	}
	if agentWorking(app.ThreadView{}) {
		t.Fatal("an idle thread is not working")
	}
	waiting := running
	waiting.Permissions = []app.Permission{{ID: "p"}}
	if agentWorking(waiting) {
		t.Fatal("waiting for the user is not work")
	}
	loading := running
	loading.Configuring = true
	if agentWorking(loading) {
		t.Fatal("a session request is not the agent working")
	}
	failed := running
	failed.Error = "boom"
	if agentWorking(failed) {
		t.Fatal("a failed thread is not working")
	}
}
