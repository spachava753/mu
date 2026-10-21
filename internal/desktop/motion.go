package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Keep motion brief and tied to an interaction. In particular, don't animate
// transcript layout on every streamed token. MyGo honors ReduceMotion here.
var (
	sidebarMotion = ui.Animation{Duration: 220 * time.Millisecond, Ease: ui.EaseInOut}
	colorMotion   = ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond}
	fadeMotion    = ui.ElementTransition{
		Colors: true, Duration: 160 * time.Millisecond,
		Enter: &ui.Motion{}, Exit: &ui.Motion{},
	}
	popupMotion = ui.ElementTransition{
		Colors: true, Duration: 160 * time.Millisecond,
		Enter: &ui.Motion{Scale: 0.98}, Exit: &ui.Motion{Scale: 0.98},
	}
)

func modal(c *ui.Context, open *bool, draw func()) ui.Element {
	return ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		// Animate the backdrop and its dialog as one surface. Animating only
		// the panel wouldn't enter: its backdrop parent is also newly built.
		backdrop.Background(ui.RGBA(0, 0, 0, 0.4)).Transition(fadeMotion)
		w, h := c.Size()
		panel.Padding(18).Gap(10).Radius(6).Border(1, c.Theme().Border).
			Background(c.Theme().Background).MaxWidth(w - 36).MaxHeight(h - 36)
		draw()
	})
}
