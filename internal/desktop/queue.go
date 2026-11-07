package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

const (
	queuedPromptHeight = 30
	queuedPromptInset  = 12
	// queueHeight is three prompts and part of a fourth to show there are more, so
	// that a long queue leaves the draft in the window.
	queueRows       = 3
	queueHeight     = (queueRows+0.5)*queuedPromptHeight + queueRows*queueGap
	queueGap        = 4
	scrollbarGutter = 10
)

// promptQueue shows the prompts waiting for a turn, next first, just above the draft
// so that it stays where it was. Each can go now instead, or be taken out.
func (v *View) promptQueue(c *ui.Context, thread app.ThreadView) {
	if len(thread.Queue) == 0 {
		return
	}
	ui.Column(c.Key("prompt-queue")).Shrink(0).Gap(queueGap).Transition(queueMotion).Children(func() {
		if thread.QueueHeld && !thread.Queue[0].Steering {
			// Lined up with the prompts' text.
			ui.Text(c, "Paused. These continue after your next message.").FontSize(12).TextColor(faint(c.Theme())).Padding(0, queuedPromptInset+1)
		}
		list := ui.Scroll(c.Key("queued-prompts")).Label("Queued prompts").Shrink(0).MaxHeight(queueHeight).Gap(queueGap)
		if len(thread.Queue) > queueRows {
			list.Padding(0, scrollbarGutter, 0, 0) // The scroll bar doesn't cover the buttons.
		}
		list.Children(func() {
			for _, p := range thread.Queue {
				v.queuedPrompt(c, thread, p)
			}
		})
	})
}

func (v *View) queuedPrompt(c *ui.Context, thread app.ThreadView, p app.QueuedPrompt) {
	t := c.Theme()
	ui.Row(c.Key(p.ID)).Shrink(0).Height(queuedPromptHeight).Padding(0, 5, 0, queuedPromptInset).Gap(6).Radius(6).Border(1, hairline(t)).Transition(queueMotion).Children(func() {
		ui.Text(c, p.Preview).FontSize(12.5).SingleLine().Grow(1).MinWidth(0).Tooltip(p.Preview)
		if p.Files > 0 {
			ui.Row(c).Shrink(0).Gap(3).Label(itoa(p.Files) + " attached").Tooltip(itoa(p.Files) + " attached").Children(func() {
				ui.Icon(c, paperclipIcon).Size(12, 12).TextColor(t.TextMuted)
				ui.Text(c, itoa(p.Files)).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
			})
		}
		if p.Steering {
			ui.Spinner(c).Size(12, 12).Label("Sending").Margin(0, 2, 0, 0)
			ui.Text(c, "Sends when the turn stops").FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(0)
		} else {
			action, tip := "Steer", "Interrupt this turn and send this now"
			switch {
			case !thread.Running:
				action, tip = "Send", "Send this now"
			case thread.Stopping:
				action, tip = "Send", "Send this when the turn stops"
			}
			quietAction(c, action, action+": "+p.Preview, false).Tooltip(tip).OnClick(func() {
				v.fail(v.app.SteerQueued(thread.ID, p.ID))
				v.picker.refocus = true
				if list := v.lists[thread.ID]; list != nil {
					list.ScrollToEnd()
				}
			})
		}
		smallIconButton(c, "Remove from queue: "+p.Preview, closeIcon).Tooltip("Remove from queue").OnClick(func() {
			v.app.RemoveQueued(thread.ID, p.ID)
			v.picker.refocus = true
		})
	})
}
