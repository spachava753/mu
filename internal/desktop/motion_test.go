package desktop_test

import (
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
)

func rawClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame() // Do not advance the clock: inspect the start of the transition.
}
func sidebarWidth(t *testing.T, tt *ui.Tester) float32 {
	t.Helper()
	r, ok := tt.Find("Thread sidebar")
	if !ok {
		t.Fatal("missing sidebar")
	}
	return r.W
}

func TestSidebarAnimationAndReversal(t *testing.T) {
	const expanded, collapsed = 232, 48
	a := testApp(t)
	_, err := a.Create("unused-agent", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := desktop.New(a, "", "")
	tt := ui.NewTester(v.Draw, 800, 600)
	if got := sidebarWidth(t, tt); got != expanded {
		t.Fatal("initial layout should not animate", got)
	}
	rawClick(t, tt, "Collapse sidebar")
	if got := sidebarWidth(t, tt); got != expanded {
		t.Fatal("sidebar snapped at the start", got)
	}
	tt.Advance(90 * time.Millisecond)
	tt.Frame() // Sample at Now(), not the last 60 Hz refresh before it.
	mid := sidebarWidth(t, tt)
	if mid <= collapsed || mid >= expanded {
		t.Fatal("no intermediate sidebar width", mid)
	}
	rawClick(t, tt, "Expand sidebar")
	if got := sidebarWidth(t, tt); got != mid {
		t.Fatal("reversing snapped instead of continuing", mid, got)
	}
	tt.Advance(90 * time.Millisecond)
	if got := sidebarWidth(t, tt); got <= mid || got >= expanded {
		t.Fatal("reversal did not animate", got)
	}
	tt.Advance(300 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != expanded {
		t.Fatal("did not settle", got)
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	rawClick(t, tt, "Collapse sidebar")
	if got := sidebarWidth(t, tt); got != collapsed {
		t.Fatal("ignored reduced motion", got)
	}
}

func TestDialogAndMenuAnimate(t *testing.T) {
	a, tt := composerFixture(t, 800, 600)
	// Prepare the session so asynchronous setup cannot affect animation samples.
	if err := a.Connect(a.Snapshot().Threads[0].ID); err != nil {
		t.Fatal(err)
	}
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	pixel := func() any { return tt.Image().At(100, 25) }
	before := pixel()
	rawClick(t, tt, "Session options")
	start := pixel()
	tt.Advance(70 * time.Millisecond)
	middle := pixel()
	tt.Advance(200 * time.Millisecond)
	end := pixel()
	if start != before || middle == start || middle == end {
		t.Fatal("modal did not fade through an intermediate frame", before, start, middle, end)
	}
	rawClick(t, tt, "Set Model")
	r0, _ := tt.Find("Set Model choices")
	tt.Advance(70 * time.Millisecond)
	r1, _ := tt.Find("Set Model choices")
	tt.Advance(200 * time.Millisecond)
	r2, _ := tt.Find("Set Model choices")
	if !(r0.W < r1.W && r1.W < r2.W) {
		t.Fatalf("menu entrance skipped: %v / %v / %v", r0, r1, r2)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(200 * time.Millisecond)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(70 * time.Millisecond)
	closing := pixel()
	tt.Advance(200 * time.Millisecond)
	if closing == before || closing == end || pixel() != before {
		t.Fatal("modal exit did not fade back to the original view")
	}
}

// Optional visual artifact: deterministic native-renderer frames, never desktop
// input, credentials, or real provider traffic.
func TestRecordMotion(t *testing.T) {
	dir := os.Getenv("MU_QA_RENDER_DIR")
	if dir == "" {
		t.Skip("set MU_QA_RENDER_DIR to record native UI motion")
	}
	a, tt := composerFixture(t, 800, 600)
	if err := a.Connect(a.Snapshot().Threads[0].ID); err != nil {
		t.Fatal(err)
	}
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	animation := &gif.GIF{LoopCount: 0}
	frames := func(n int) {
		for range n {
			frame := image.NewPaletted(tt.Image().Bounds(), palette.Plan9)
			draw.FloydSteinberg.Draw(frame, frame.Bounds(), tt.Image(), image.Point{})
			animation.Image = append(animation.Image, frame)
			animation.Delay = append(animation.Delay, 4)
			tt.Advance(40 * time.Millisecond)
		}
	}
	frames(8)
	rawClick(t, tt, "Collapse sidebar")
	frames(12)
	rawClick(t, tt, "Expand sidebar")
	frames(12)
	rawClick(t, tt, "Message")
	tt.Type("/")
	frames(12)
	tt.Key(0, ui.KeyEscape)
	frames(8)
	rawClick(t, tt, "Session options")
	frames(12)
	rawClick(t, tt, "Set Model")
	frames(12)
	tt.Key(0, ui.KeyEnd)
	frames(8)
	tt.Key(0, ui.KeyEscape)
	frames(8)
	tt.Key(0, ui.KeyEscape)
	frames(12)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "mu-motion.gif"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, animation); err != nil {
		t.Fatal(err)
	}
}
