package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/state"
)

// Keep the context window visible beside the draft, not buried in settings.
func (v *View) usageLine(c *ui.Context, s state.Session) {
	ui.Row(c).Gap(12).Wrap().Children(func() {
		indicator := ui.ButtonBase(c).Transition(colorMotion).Label("Context usage").Tooltip("Context window usage · click for token details").Padding(6, 10).Radius(6).Border(1, c.Theme().Border).Background(c.Theme().Surface)
		indicator.Children(func() {
			ui.Column(c).Width(284).Gap(5).Children(func() {
				label := "Context not reported"
				if s.Usage != nil {
					label = contextLabel(s.Usage)
				}
				ui.Text(c, label).FontSize(12)
				if u := s.Usage; u != nil && u.Size > 0 {
					target := float32(min(1, max(0, float64(u.Used)/float64(u.Size))))
					ui.Progress(c, float64(indicator.AnimateTo("context-fill", target, sidebarMotion))).Height(4).FillWidth().Label("Context window usage")
				}
			})
		}).OnClick(func() { v.usageOpen = !v.usageOpen })
		if u := s.Usage; u != nil && u.Cost != nil {
			ui.Text(c, fmt.Sprintf("Cost %s %.4f", u.Cost.Currency, u.Cost.Amount)).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		ui.PopoverBase(c, indicator, &v.usageOpen, func(panel ui.Element) {
			panel.Transition(popupMotion).AttachTo(indicator, ui.AnchorTopStart, ui.AnchorBottomStart).Width(350).Margin(0, 0, 6, 0).Padding(12).Radius(6).Border(1, c.Theme().Border).Background(c.Theme().Background)
			ui.Scroll(c).MaxHeight(260).Gap(8).Children(func() {
				ui.Text(c, "Token usage").Bold()
				if u := s.Usage; u != nil {
					ui.Text(c, contextLabel(u)).FontSize(12)
					if u.Cost != nil {
						ui.Text(c, fmt.Sprintf("Session cost: %s %.4f", u.Cost.Currency, u.Cost.Amount)).FontSize(12)
					}
				} else {
					ui.Text(c, "The agent has not reported context usage.").FontSize(12).TextColor(c.Theme().TextMuted)
				}
				if u := s.TurnUsage; u != nil {
					ui.Text(c, "Last turn").Bold().FontSize(12)
					ui.Text(c, fmt.Sprintf("%s input · %s output · %s total", count(u.InputTokens), count(u.OutputTokens), count(u.TotalTokens))).FontSize(12)
					if u.ThoughtTokens != nil {
						ui.Text(c, "Thinking tokens: "+count(*u.ThoughtTokens)).FontSize(12)
					}
					if u.CachedReadTokens != nil {
						ui.Text(c, "Cache read: "+count(*u.CachedReadTokens)).FontSize(12)
					}
					if u.CachedWriteTokens != nil {
						ui.Text(c, "Cache write: "+count(*u.CachedWriteTokens)).FontSize(12)
					}
				}
				if s.StopReason != "" {
					ui.Text(c, "Last stop: "+string(s.StopReason)).FontSize(12).TextColor(c.Theme().TextMuted)
				}
			})
		})
	})
}
