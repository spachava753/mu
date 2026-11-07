package desktop

import (
	"encoding/json"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

// usageColor warns as the context window fills.
func usageColor(t *ui.Theme, fraction float32) ui.Color {
	switch {
	case fraction >= 0.9:
		return t.Danger
	case fraction >= 0.7:
		return warnColor(t)
	}
	return t.TextMuted
}

// Keep the context window visible beside the draft, not buried in settings.
func (v *View) usageLine(c *ui.Context, thread app.ThreadView, hint bool) {
	t := c.Theme()
	s := thread.Session
	ui.Row(c).Padding(0, 2).Gap(10).Children(func() {
		indicator := ui.ButtonBase(c).Transition(colorMotion).Label("Context usage").Tooltip("Context window usage · click for token details").Padding(3, 6).Radius(5).Gap(8).Shrink(0)
		if indicator.Hovered() || v.usageOpen {
			indicator.Background(t.Text.Alpha(0.05))
		}
		indicator.Children(func() {
			if u := s.Usage; u != nil && u.Size > 0 {
				target := float32(min(1, max(0, float64(u.Used)/float64(u.Size))))
				fill := indicator.AnimateTo("context-fill", target, sidebarMotion)
				ui.Box(c).Label("Context window usage").Width(40).Height(4).Radius(2).Background(t.Text.Alpha(0.1)).Children(func() {
					ui.Box(c).Width(40 * fill).Height(4).Radius(2).Background(usageColor(t, target))
				})
			}
			label := "Context not reported"
			if s.Usage != nil {
				label = contextLabel(s.Usage)
			}
			ui.Text(c, label).FontSize(12).TextColor(faint(t)).SingleLine()
		}).OnClick(func() { v.usageOpen = !v.usageOpen })
		if u := s.Usage; u != nil && u.Cost != nil {
			ui.Text(c, fmt.Sprintf("Cost %s %.4f", u.Cost.Currency, u.Cost.Amount)).FontSize(12).TextColor(faint(t)).SingleLine().Shrink(0)
		}
		ui.Spacer(c)
		// Says why Enter does nothing during a turn, whatever the width.
		if thread.Running {
			ui.Text(c, "Send when this turn ends").FontSize(12).TextColor(faint(t)).SingleLine().Shrink(1).MinWidth(0)
		} else if hint {
			ui.Text(c, "Enter to send · Shift+Enter for a new line").FontSize(12).TextColor(faint(t)).SingleLine().Shrink(1).MinWidth(0)
		}
		ui.PopoverBase(c, indicator, &v.usageOpen, func(panel ui.Element) {
			panel.Transition(popupMotion).AttachTo(indicator, ui.AnchorTopStart, ui.AnchorBottomStart).Width(320).Margin(0, 0, 6, 0).Padding(12, 14).Radius(8).Border(1, hairline(t)).Background(t.Surface)
			ui.Scroll(c).MaxHeight(280).Gap(10).Children(func() {
				ui.Text(c, "Token usage").FontSize(13).FontWeight(500)
				row := func(name, value string) {
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, name).FontSize(12).TextColor(t.TextMuted).Grow(1)
						ui.Text(c, value).FontSize(12)
					})
				}
				if u := s.Usage; u != nil {
					ui.Text(c, contextLabel(u)).FontSize(12)
					if u.Cost != nil {
						row("Session cost", fmt.Sprintf("%s %.4f", u.Cost.Currency, u.Cost.Amount))
					}
				} else {
					ui.Text(c, "The agent has not reported context usage.").FontSize(12).TextColor(t.TextMuted)
				}
				if u := s.TurnUsage; u != nil {
					ui.Column(c).Gap(5).Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
						ui.Text(c, "Last turn").FontSize(12).FontWeight(500).TextColor(faint(t)).Margin(0, 0, 2, 0)
						row("Input", count(u.InputTokens))
						row("Output", count(u.OutputTokens))
						if u.ThoughtTokens != nil {
							row("Thinking", count(*u.ThoughtTokens))
						}
						if u.CachedReadTokens != nil {
							row("Cache read", count(*u.CachedReadTokens))
						}
						if u.CachedWriteTokens != nil {
							row("Cache write", count(*u.CachedWriteTokens))
						}
						row("Total", count(u.TotalTokens))
					})
				}
				if s.StopReason != "" {
					ui.Text(c, "Last stop: "+string(s.StopReason)).FontSize(11.5).TextColor(t.TextMuted)
				}
				if s.AgentUpdatedAt != "" {
					ui.Text(c, "Agent updated: "+s.AgentUpdatedAt).FontSize(11.5).TextColor(t.TextMuted)
				}
				if len(s.Metadata) > 0 {
					smallDisclosure(c, "Agent metadata", v.disclosure("metadata"+thread.ID), func() {
						b, _ := json.MarshalIndent(s.Metadata, "", "  ")
						detailText(c, string(b))
					})
				}
			})
		})
	})
}
