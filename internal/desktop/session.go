package desktop

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

func count(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func contextLabel(u *acp.SessionUsageUpdate) string {
	if u.Size <= 0 {
		return "Context " + count(u.Used) + " / unknown limit"
	}
	return fmt.Sprintf("Context %s / %s (%.1f%%)", count(u.Used), count(u.Size), 100*float64(u.Used)/float64(u.Size))
}

// Drop only a redundant display prefix matching the surrounding field label.
// Protocol values and unrelated labels (including provider/model names) stay intact.
func optionLabel(field, label string) string {
	prefix, rest, ok := strings.Cut(label, ":")
	if ok && strings.EqualFold(strings.TrimSpace(prefix), strings.TrimSpace(field)) && strings.TrimSpace(rest) != "" {
		return strings.TrimSpace(rest)
	}
	return label
}

func (v *View) sessionDialog(c *ui.Context, s app.Snapshot) {
	var thread *app.ThreadView
	for i := range s.Threads {
		if s.Threads[i].ID == v.selected {
			thread = &s.Threads[i]
			break
		}
	}
	if thread == nil {
		v.sessionOpen = false
	}
	modal(c, &v.sessionOpen, func() {
		if thread == nil {
			return
		}
		t := *thread
		th := c.Theme()
		_, height := c.Size()
		ui.Row(c).Width(440).Gap(8).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, "Session options").FontSize(14).FontWeight(500)
				agent := t.AgentName
				if agent == "" {
					agent = commandName(t.Command)
				}
				ui.Text(c, "Offered by "+agent+" for this session").FontSize(11.5).TextColor(th.TextMuted).SingleLine()
			})
			iconButton(c, "Close options", closeIcon).OnClick(func() { v.sessionOpen = false })
		})
		ui.Scroll(c.Key("session-options-"+t.ID)).Width(440).MaxHeight(max(120, min(440, float32(height)-160))).Gap(16).Padding(4, 2).Children(func() {
			if t.Configuring {
				ui.Row(c).Gap(8).Children(func() {
					ui.Spinner(c).Size(13, 13)
					ui.Text(c, "Updating session…").FontSize(12).TextColor(th.TextMuted)
				})
			}
			if !t.Connected {
				ui.Text(c, "Connect to load the options offered by this agent. No prompt is sent.").FontSize(12).TextColor(th.TextMuted)
				ui.Button(c, "Connect session").AlignSelf(ui.Start).Disabled(t.Configuring || t.Archived).OnClick(func() { v.fail(v.app.Connect(t.ID)) })
			}
			if t.SessionError != "" {
				ui.Text(c, t.SessionError).Selectable().TextColor(th.Danger).FontSize(12)
			}
			if v.err != "" {
				ui.Text(c, v.err).TextColor(th.Danger).FontSize(12)
			}
			disabled := !t.Connected || t.Configuring || t.Archived
			for _, option := range t.Session.Options {
				if o := option.Select; o != nil {
					ui.Column(c.Key(string(o.Id))).Children(func() {
						description := ""
						if o.Description != nil {
							description = *o.Description
						}
						choices := selectChoices(o)
						field(c, o.Name, description, func() {
							selectChoice(c, "Set "+o.Name, string(o.CurrentValue), choices, disabled || len(choices) == 0, func(value string) { v.fail(v.app.SetOption(t.ID, string(o.Id), value)) })
						})
					})
				}
				if o := option.Boolean; o != nil {
					ui.Column(c.Key(string(o.Id))).Gap(4).Children(func() {
						value := o.CurrentValue
						if ui.Checkbox(c, &value, o.Name).Disabled(disabled).Changed() {
							v.fail(v.app.SetOption(t.ID, string(o.Id), value))
						}
						if o.Description != nil {
							ui.Text(c, *o.Description).FontSize(11.5).TextColor(th.TextMuted)
						}
					})
				}
			}
			if len(t.Session.Options) == 0 && t.Session.Modes != nil {
				field(c, "Mode", "", func() {
					selectChoice(c, "Set mode", string(t.Session.Modes.CurrentModeId), modeChoices(t.Session.Modes), disabled, func(value string) { v.fail(v.app.SetMode(t.ID, value)) })
				})
			}
			if t.Connected && len(t.Session.Options) == 0 && t.Session.Modes == nil {
				ui.Text(c, "This agent has not reported configurable session options.").FontSize(12).TextColor(th.TextMuted)
			}
			if t.Session.AgentUpdatedAt != "" || len(t.Session.Metadata) > 0 {
				ui.Column(c).Gap(6).Padding(10, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(th.Border).Children(func() {
					if t.Session.AgentUpdatedAt != "" {
						ui.Text(c, "Agent updated: "+t.Session.AgentUpdatedAt).FontSize(11.5).TextColor(th.TextMuted)
					}
					if len(t.Session.Metadata) > 0 {
						smallDisclosure(c, "Agent metadata", v.disclosure("metadata"+t.ID), func() {
							b, _ := json.MarshalIndent(t.Session.Metadata, "", "  ")
							detailText(c, string(b))
						})
					}
				})
			}
		})
	})
}
