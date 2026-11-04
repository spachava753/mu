package desktop_test

import (
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/testutil"
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
	_, err := a.Create("unused-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	v := desktop.New(a, "")
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

// dragSidebar presses the sidebar's edge and returns a function that moves the
// pointer to a window position, and one that lets go there.
func dragSidebar(t *testing.T, tt *ui.Tester) (move, release func(x float32)) {
	t.Helper()
	edge, ok := tt.Find("Resize sidebar")
	if !ok {
		t.Fatal("missing sidebar handle")
	}
	y := edge.Y + edge.H/2
	tt.Move(edge.X+edge.W/2, y)
	tt.Press(edge.X+edge.W/2, y)
	// The press starts where the sidebar's edge is, so widths read as positions.
	offset := edge.X + edge.W/2 - (edge.X + edge.W)
	return func(x float32) { tt.Move(x+offset, y); tt.Frame() }, func(x float32) { tt.Release(x+offset, y); tt.Frame() }
}

func TestSidebarDrag(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create("unused-agent", ""); err != nil {
		t.Fatal(err)
	}
	v := desktop.New(a, "")
	tt := ui.NewTester(v.Draw, 800, 600)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)

	// The edge stays under the pointer, and the width saves on release.
	move, release := dragSidebar(t, tt)
	move(300)
	if got := sidebarWidth(t, tt); got != 300 {
		t.Fatalf("width %v does not follow the pointer", got)
	}
	if a.Snapshot().SidebarWidth != 0 {
		t.Fatal("saved before release")
	}
	screenshot(t, tt, "sidebar-drag-300")
	// Up to half the window, however far the pointer goes.
	move(700)
	if got := sidebarWidth(t, tt); got != 400 {
		t.Fatalf("width %v passes half the window", got)
	}
	screenshot(t, tt, "sidebar-drag-half")
	// At the minimum, it waits for the snap point.
	move(190)
	if got := sidebarWidth(t, tt); got != 216 {
		t.Fatalf("width %v passes the minimum", got)
	}
	screenshot(t, tt, "sidebar-drag-min")
	move(260)
	release(260)
	if got := a.Snapshot(); got.SidebarWidth != 260 || got.SidebarCollapsed {
		t.Fatalf("saved %v collapsed=%v", got.SidebarWidth, got.SidebarCollapsed)
	}

	// Past the snap point it springs to the rail: through frames in between,
	// past the rail and back. Nothing saves until release.
	move, release = dragSidebar(t, tt)
	move(216)
	move(120)
	var widths []float32
	for range 30 {
		tt.Advance(16 * time.Millisecond)
		widths = append(widths, sidebarWidth(t, tt))
	}
	if widths[3] <= 48 || widths[3] >= 216 {
		t.Fatalf("snap did not animate: %v", widths[:4])
	}
	lowest := widths[0]
	for _, w := range widths {
		lowest = min(lowest, w)
	}
	if lowest >= 48 {
		t.Fatalf("snap has no spring: %v", widths)
	}
	tt.Advance(500 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 48 {
		t.Fatalf("snap settled at %v", got)
	}
	screenshot(t, tt, "sidebar-drag-snapped")
	if a.Snapshot().SidebarCollapsed {
		t.Fatal("collapse saved before release")
	}
	// Changing course springs back out, and then follows the pointer again.
	move(300)
	tt.Advance(60 * time.Millisecond)
	if got := sidebarWidth(t, tt); got <= 48 || got >= 300 {
		t.Fatalf("expanding again did not animate: %v", got)
	}
	tt.Advance(600 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 300 {
		t.Fatalf("expanded to %v", got)
	}
	move(320)
	if got := sidebarWidth(t, tt); got != 320 {
		t.Fatalf("stopped following at %v", got)
	}
	move(100)
	tt.Advance(600 * time.Millisecond)
	release(100)
	got := a.Snapshot()
	if !got.SidebarCollapsed || got.SidebarWidth != 260 {
		t.Fatalf("snapped release: width=%v collapsed=%v", got.SidebarWidth, got.SidebarCollapsed)
	}

	// The collapse button brings back the saved width.
	click(t, tt, "Expand sidebar")
	tt.Advance(300 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 260 || a.Snapshot().SidebarCollapsed {
		t.Fatalf("expanded to %v", got)
	}

	// So does dragging the rail's edge.
	click(t, tt, "Collapse sidebar")
	tt.Advance(300 * time.Millisecond)
	move, release = dragSidebar(t, tt)
	move(280)
	tt.Advance(600 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 280 {
		t.Fatalf("rail dragged to %v", got)
	}
	release(280)
	if got := a.Snapshot(); got.SidebarWidth != 280 || got.SidebarCollapsed {
		t.Fatalf("rail drag saved %v collapsed=%v", got.SidebarWidth, got.SidebarCollapsed)
	}

	// A narrower window limits the width without forgetting it.
	move, release = dragSidebar(t, tt)
	move(400)
	release(400)
	tt.SetSize(600, 600)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 300 {
		t.Fatalf("width %v in a 600 window", got)
	}
	if a.Snapshot().SidebarWidth != 400 {
		t.Fatal("a smaller window overwrote the saved width")
	}
	tt.SetSize(800, 600)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 400 {
		t.Fatalf("width %v after the window grew back", got)
	}

	// With reduced motion the snap has no frames in between.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	move, release = dragSidebar(t, tt)
	move(100)
	if got := sidebarWidth(t, tt); got != 48 {
		t.Fatalf("reduced motion snap at %v", got)
	}
	release(100)
}

// A drag ends where the pointer lets go, even if it moved no earlier.
func TestSidebarDragFinishesAtRelease(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create("unused-agent", ""); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	move, release := dragSidebar(t, tt)
	move(300)
	release(350)
	if got := a.Snapshot(); got.SidebarWidth != 350 || got.SidebarCollapsed {
		t.Fatalf("released at 350: saved %v collapsed=%v", got.SidebarWidth, got.SidebarCollapsed)
	}
	// The whole movement can come with the release.
	_, release = dragSidebar(t, tt)
	release(280)
	if got := a.Snapshot(); got.SidebarWidth != 280 {
		t.Fatalf("released at 280: saved %v", got.SidebarWidth)
	}
	// So can crossing the snap point, and the width stays saved for later.
	move, release = dragSidebar(t, tt)
	move(190)
	release(120)
	if got := a.Snapshot(); got.SidebarWidth != 280 || !got.SidebarCollapsed {
		t.Fatalf("released past the snap point: saved %v collapsed=%v", got.SidebarWidth, got.SidebarCollapsed)
	}
}

// Losing the press, as when the window loses focus, abandons the drag.
func TestSidebarDragCancelledByFocusLoss(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create("unused-agent", ""); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	move, _ := dragSidebar(t, tt)
	move(300)
	tt.SetFocused(false)
	tt.SetFocused(true)
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 232 {
		t.Fatalf("width %v after the drag was lost", got)
	}
	if got := a.Snapshot(); got.SidebarWidth != 0 || got.SidebarCollapsed {
		t.Fatalf("a lost drag saved: %v collapsed=%v", got.SidebarWidth, got.SidebarCollapsed)
	}
	click(t, tt, "Collapse sidebar")
	tt.Advance(300 * time.Millisecond)
	if got := sidebarWidth(t, tt); got != 48 {
		t.Fatalf("collapse after a lost drag left %v", got)
	}
}

// The spring keeps the sidebar within the half-window limit, and a fast drag
// does not carry it away past the rail.
func TestSidebarSpringStaysInBounds(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create("unused-agent", ""); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	check := func(frames int) {
		t.Helper()
		for range frames {
			tt.Advance(16 * time.Millisecond)
			if got := sidebarWidth(t, tt); got > 400 || got < 42 {
				t.Fatalf("width %v left its bounds", got)
			}
		}
	}
	move, release := dragSidebar(t, tt)
	for _, x := range []float32{220, 150, 80, 20} {
		move(x)
		check(1)
	}
	check(40)
	move(400)
	check(40)
	if got := sidebarWidth(t, tt); got != 400 {
		t.Fatalf("expanded to %v", got)
	}
	release(400)
}

// Grabbing the edge while it animates continues from where it shows.
func TestSidebarDragTakesOverAnimation(t *testing.T) {
	a := testApp(t)
	if _, err := a.Create("unused-agent", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.SetSidebar(400, false); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(desktop.New(a, "").Draw, 800, 600)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	rawClick(t, tt, "Collapse sidebar")
	tt.Advance(70 * time.Millisecond)
	tt.Frame() // Bring the frame up to the clock, so that the pointer finds the edge.
	shown := sidebarWidth(t, tt)
	if shown <= 216 || shown >= 400 {
		t.Fatalf("not partway: %v", shown)
	}
	move, release := dragSidebar(t, tt)
	move(shown + 20)
	if got := sidebarWidth(t, tt); got != shown+20 {
		t.Fatalf("the edge jumped from %v to %v", shown, got)
	}
	release(shown + 20)
	if got := a.Snapshot(); got.SidebarCollapsed || got.SidebarWidth != shown+20 {
		t.Fatalf("saved %v collapsed=%v", got.SidebarWidth, got.SidebarCollapsed)
	}
}

func TestDialogAndMenuAnimate(t *testing.T) {
	a, tt := composerFixture(t, 800, 600)
	// Prepare the session so asynchronous setup cannot affect animation samples.
	if err := a.Connect(a.Snapshot().Threads[0].ID); err != nil {
		t.Fatal(err)
	}
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	rawClick(t, tt, "Model")
	r0, _ := tt.Find("Model choices")
	tt.Advance(70 * time.Millisecond)
	r1, _ := tt.Find("Model choices")
	tt.Advance(200 * time.Millisecond)
	r2, _ := tt.Find("Model choices")
	if !(r0.W < r1.W && r1.W < r2.W) {
		t.Fatalf("menu entrance skipped: %v / %v / %v", r0, r1, r2)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(200 * time.Millisecond)
	pixel := func() any { return tt.Image().At(100, 25) }
	before := pixel()
	rawClick(t, tt, "New project")
	start := pixel()
	tt.Advance(70 * time.Millisecond)
	middle := pixel()
	tt.Advance(200 * time.Millisecond)
	end := pixel()
	if start != before || middle == start || middle == end {
		t.Fatal("modal did not fade through an intermediate frame", before, start, middle, end)
	}
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
	rawClick(t, tt, "Model")
	frames(12)
	tt.Key(0, ui.KeyEnd)
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

// While an agent works, its status text shimmers: frames differ only in color,
// the text stays put, and it stops with the turn. With reduced motion it is
// still.
func TestWorkingTextShimmers(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(fmt.Sprintf("reduced=%v", reduced), func(t *testing.T) {
			a := testApp(t)
			id, err := a.Create(testutil.Agent(t), "")
			if err != nil {
				t.Fatal(err)
			}
			view := desktop.New(a, "")
			tt := ui.NewTester(view.Draw, 1100, 780)
			tt.SetPreferences(ui.Preferences{ReduceMotion: reduced})
			tt.Frame()
			if err := a.Send(id, "wait"); err != nil {
				t.Fatal(err)
			}
			waitView(t, a, tt, func(s app.Snapshot) bool {
				th := s.Threads[0]
				return th.Running && th.Status == "Working…" && len(th.Entries) > 1
			})
			box, ok := tt.Find("Working…")
			if !ok {
				t.Fatal("no working text", tt.Texts())
			}
			crop := func() string {
				img := tt.Image()
				var b strings.Builder
				for y := int(box.Y); y < int(box.Y+box.H); y++ {
					for x := int(box.X); x < int(box.X+box.W); x++ {
						c := img.RGBAAt(x, y)
						fmt.Fprintf(&b, "%d,%d,%d;", c.R, c.G, c.B)
					}
				}
				return b.String()
			}
			frames := map[string]bool{}
			for i := range 12 {
				tt.Advance(150 * time.Millisecond)
				frames[crop()] = true
				if moved, ok := tt.Find("Working…"); !ok || moved != box {
					t.Fatalf("frame %d: the text moved: %+v to %+v", i, box, moved)
				}
				screenshot(t, tt, fmt.Sprintf("shimmer-reduced-%v-%02d", reduced, i))
			}
			if reduced && len(frames) != 1 {
				t.Fatalf("reduced motion still animates: %d distinct frames", len(frames))
			}
			if !reduced && len(frames) < 4 {
				t.Fatalf("the text does not shimmer: %d distinct frames", len(frames))
			}
			a.Stop(id)
			waitView(t, a, tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
			if tt.HasText("Working…") {
				t.Fatal("working text outlived the turn")
			}
		})
	}
}
