package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

const (
	sidebarDefaultWidth  = 232
	sidebarMinWidth      = 216 // the narrowest its header and bulk-action rows still read
	railWidth            = 48
	sidebarSnapWidth     = 176 // dragging below this collapses to the rail
	sidebarSnapRelease   = 192 // and dragging back past this expands again
	sidebarHandleWidth   = 6
	sidebarSnapOvershoot = 6 // how far the spring may carry it past the rail
)

// sidebarSnapMotion carries the sidebar between the rail and its width when a
// drag crosses the snap point.
var sidebarSnapMotion = ui.Animation{Spring: ui.SpringOptions{Duration: 320 * time.Millisecond, Bounce: 0.25, Precision: 0.25}}

// sidebarLimit is the widest the sidebar may be in a window: half of it, though
// never less than the minimum.
func sidebarLimit(window float32) float32 { return max(sidebarMinWidth, window/2) }

// fitSidebar is the width to show for a saved width in a window, and the default
// when none is saved.
func fitSidebar(width, window float32) float32 {
	if width <= 0 {
		width = sidebarDefaultWidth
	}
	return max(sidebarMinWidth, min(width, sidebarLimit(window)))
}

// sidebarDrag follows the pointer dragging the sidebar's edge.
type sidebarDrag struct {
	held    bool    // the pointer has the handle
	grab    float32 // from the pointer to the edge, so that the edge does not jump
	active  bool    // the pointer has moved it
	pointer float32 // where the pointer holds the edge, whatever the limits
	snapped bool    // collapsing to the rail
}

// start begins moving the edge from where it shows, which may be partway through
// an animation.
func (d *sidebarDrag) start(shown float32) {
	d.active, d.pointer, d.snapped = true, shown, shown < sidebarSnapWidth
}

// move puts the edge at a position and reports whether that crossed the snap
// point. The two thresholds differ so that a pointer resting on one cannot make
// the sidebar flicker between its shapes.
func (d *sidebarDrag) move(to float32) (crossed bool) {
	d.pointer = to
	switch {
	case d.snapped && to >= sidebarSnapRelease:
		d.snapped = false
		return true
	case !d.snapped && to < sidebarSnapWidth:
		d.snapped = true
		return true
	}
	return false
}

// sidebarHandle builds the sidebar's draggable edge, and returns the width of
// its thread list and whether the rail shows instead. The sidebar's width
// animates only across the snap point; otherwise it stays under the pointer.
func (v *View) sidebarHandle(c *ui.Context, sidebar ui.Element, s app.Snapshot) (width float32, collapsed bool) {
	window, _ := c.Size()
	width, collapsed = fitSidebar(s.SidebarWidth, window), s.SidebarCollapsed
	handle := ui.Box(c.Key("sidebar-handle")).Absolute().Top(0).Bottom(0).Right(0).Width(sidebarHandleWidth).Label("Resize sidebar").Cursor(ui.CursorResizeEW)
	dx, _, held := handle.Dragged()
	_, _, released := handle.DragEnded()
	switch {
	case held || released:
		// The sidebar starts at the window's edge and the handle moves with it,
		// so the pointer's position in the sidebar is the one to go by. It also
		// covers the movement that came with the release.
		x, _, _ := sidebar.PointerPosition()
		if !v.drag.held {
			// The grab is where the press began, which may be a move before this
			// frame.
			v.drag.held, v.drag.grab = true, v.sidebarShown-(x-dx)
		}
		to := x + v.drag.grab
		if !v.drag.active && to != v.sidebarShown {
			v.drag.start(v.sidebarShown)
		}
		if v.drag.active {
			v.springing = v.drag.move(to) || v.springing
		}
	case v.drag.held:
		// The press ended without a release, as when the window loses focus. The
		// gesture did not finish, so it saves nothing.
		v.springing = v.springing || v.drag.active
		v.drag = sidebarDrag{}
	}
	following := v.drag.active
	if following {
		collapsed = v.drag.snapped
		if !collapsed {
			width = fitSidebar(v.drag.pointer, window)
		}
	}
	if released {
		if following {
			if collapsed {
				v.selection.clear() // The rail has no way to show or act on it.
			}
			saved := s.SidebarWidth
			if !collapsed {
				saved = width
			}
			if saved != s.SidebarWidth || collapsed != s.SidebarCollapsed {
				v.fail(v.app.SetSidebar(saved, collapsed))
			}
		}
		v.drag = sidebarDrag{}
	}
	target := width
	if collapsed {
		target = railWidth
	}
	var shown float32
	switch {
	case v.springing:
		shown = sidebar.AnimateTo("width", target, sidebarSnapMotion)
		v.springing = shown != target
		shown = max(shown, railWidth-sidebarSnapOvershoot) // A fast drag must not spring it away.
	case following && v.sidebarShown < sidebarMinWidth && abs(v.sidebarShown-target) >= 0.5:
		// Grabbed where the width cannot stay, as below the minimum partway
		// through an animation. A spring would keep the speed it had.
		shown = sidebar.AnimateTo("width", target, sidebarMotion)
	case following:
		shown = sidebar.Jump("width", target)
	default:
		shown = sidebar.AnimateTo("width", target, sidebarMotion)
	}
	shown = min(shown, sidebarLimit(window))
	v.sidebarShown = shown
	sidebar.Width(shown)
	line := ui.Transparent
	if handle.Hovered() || v.drag.held {
		line = c.Theme().Text.Alpha(0.2)
	}
	handle.Children(func() {
		ui.Box(c).Absolute().Top(0).Bottom(0).Right(0).Width(2).Transition(colorMotion).Background(line)
	})
	return width, collapsed
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
