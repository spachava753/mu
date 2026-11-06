package desktop

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

const (
	shimmerPeriod = 1800 * time.Millisecond // one sweep across the text
	shimmerFrame  = 33 * time.Millisecond   // enough for a soft band to move smoothly
	shimmerBand   = 3.5                     // half the band's width, in characters
)

// shimmerPhase is how far through a sweep the time is, from 0 up to 1.
func shimmerPhase(now time.Time) float64 {
	return float64(now.UnixNano()%int64(shimmerPeriod)) / float64(shimmerPeriod)
}

// shimmerLevel is how much of the highlight color character i of n takes at a
// phase. A soft band sweeps from before the first character to past the last,
// so each sweep starts and ends with the text at rest.
func shimmerLevel(i, n int, phase float64) float32 {
	start, end := -shimmerBand, float64(n)+shimmerBand
	distance := math.Abs(float64(i)-(start+phase*(end-start))) / shimmerBand
	if distance >= 1 {
		return 0
	}
	return float32((1 + math.Cos(math.Pi*distance)) / 2)
}

// shimmerText shows text with a band of highlight sweeping across base, for an
// agent at work. The element lays out invisible text, and only its painter
// moves: repainting needs no view build, and stops while the text is out of
// view. The painter shapes the text in the element's own font, so the glyphs
// sit where plain text would. When the desktop asks for less motion, the text
// is still.
func shimmerText(c *ui.Context, text string, size float32, base, highlight ui.Color) ui.Element {
	if c.Preferences().ReduceMotion {
		return ui.Text(c, text).FontSize(size).TextColor(base).Label(text)
	}
	font := ui.Font{Family: c.Theme().Font, Size: size}
	runes := []rune(text)
	spans := make([]ui.Span, len(runes))
	return ui.Text(c, text).FontSize(size).TextColor(base.Alpha(0)).Label(text).Draw(func(p *ui.Painter, r ui.Rect) {
		phase := shimmerPhase(p.Now())
		for i, char := range runes {
			spans[i] = ui.Span{Text: string(char), Color: base.Mix(highlight, shimmerLevel(i, len(runes), phase))}
		}
		// Clipped only at the sides, as the element may be narrower than the text:
		// some fonts' descenders reach below the text's box, as plain text shows them.
		box := ui.Rect{X: r.X, Y: r.Y - size, W: r.W, H: r.H + 2*size}
		p.Clip(box, 0, func() { p.TextLayout(ui.ShapeRichText(spans, font, 0), r.X, r.Y, base) })
		p.After(shimmerFrame)
	})
}

// agentWorking reports whether the agent is working on the thread's turn, as
// opposed to waiting for the user or showing a more urgent state.
func agentWorking(t app.ThreadView) bool {
	return t.Running && t.Error == "" && len(t.Permissions) == 0 && !t.Configuring
}
