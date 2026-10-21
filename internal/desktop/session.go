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
		ui.Row(c).Width(480).Gap(8).Children(func() {
			ui.Text(c, "Session options").FontSize(18).Bold().Grow(1)
			ui.Button(c, "Close options").OnClick(func() { v.sessionOpen = false })
		})
		ui.Scroll(c.Key("session-options-"+t.ID)).Width(480).Height(360).Gap(14).Padding(0, 4).Children(func() {
			if t.Configuring {
				ui.Text(c, "Updating session…").TextColor(c.Theme().TextMuted)
			}
			if !t.Connected {
				ui.Text(c, "Connect to load the options offered by this agent. No prompt is sent.").FontSize(12).TextColor(c.Theme().TextMuted)
				ui.Button(c, "Connect session").Disabled(t.Configuring || t.Archived).OnClick(func() { v.fail(v.app.Connect(t.ID)) })
			}
			if t.SessionError != "" {
				ui.Text(c, t.SessionError).Selectable().TextColor(c.Theme().Danger).FontSize(12)
			}
			if v.err != "" {
				ui.Text(c, v.err).TextColor(c.Theme().Danger).FontSize(12)
			}
			disabled := !t.Connected || t.Configuring || t.Archived
			for _, option := range t.Session.Options {
				if o := option.Select; o != nil {
					ui.Column(c.Key(string(o.Id))).Gap(6).Children(func() {
						ui.Text(c, o.Name).Bold().FontSize(12)
						choices := []choice{}
						add := func(values []acp.SessionConfigSelectOption, group string) {
							for _, v := range values {
								item := choice{ID: string(v.Value), Name: optionLabel(o.Name, v.Name), Group: group}
								if v.Description != nil {
									item.Description = *v.Description
								}
								choices = append(choices, item)
							}
						}
						if o.Options.Ungrouped != nil {
							add(*o.Options.Ungrouped, "")
						}
						if o.Options.Grouped != nil {
							for _, g := range *o.Options.Grouped {
								add(g.Options, g.Name)
							}
						}
						selectChoice(c, "Set "+o.Name, string(o.CurrentValue), choices, disabled || len(choices) == 0, func(value string) { v.fail(v.app.SetOption(t.ID, string(o.Id), value)) })
						if o.Description != nil {
							ui.Text(c, *o.Description).FontSize(11).TextColor(c.Theme().TextMuted)
						}
					})
				}
				if o := option.Boolean; o != nil {
					value := o.CurrentValue
					if ui.Checkbox(c.Key(string(o.Id)), &value, o.Name).Disabled(disabled).Changed() {
						v.fail(v.app.SetOption(t.ID, string(o.Id), value))
					}
					if o.Description != nil {
						ui.Text(c, *o.Description).FontSize(11).TextColor(c.Theme().TextMuted)
					}
				}
			}
			if len(t.Session.Options) == 0 && t.Session.Modes != nil {
				ui.Text(c, "Mode").Bold().FontSize(12)
				choices := []choice{}
				for _, m := range t.Session.Modes.AvailableModes {
					item := choice{ID: string(m.Id), Name: m.Name}
					if m.Description != nil {
						item.Description = *m.Description
					}
					choices = append(choices, item)
				}
				selectChoice(c, "Set mode", string(t.Session.Modes.CurrentModeId), choices, disabled, func(value string) { v.fail(v.app.SetMode(t.ID, value)) })
			}
			if t.Connected && len(t.Session.Options) == 0 && t.Session.Modes == nil {
				ui.Text(c, "This agent has not reported configurable session options.").FontSize(12).TextColor(c.Theme().TextMuted)
			}
			if t.Session.AgentUpdatedAt != "" {
				ui.Text(c, "Agent updated: "+t.Session.AgentUpdatedAt).FontSize(11).TextColor(c.Theme().TextMuted)
			}
			if len(t.Session.Metadata) > 0 {
				ui.Collapsible(c, "Agent metadata", v.disclosure("metadata"+t.ID), func() {
					b, _ := json.MarshalIndent(t.Session.Metadata, "", "  ")
					ui.Text(c, string(b)).Selectable().Font("monospace").FontSize(11)
				})
			}
		})
	}).Border(1, c.Theme().Border).Shadow(0, 0, 0, 0, ui.Transparent)
}
