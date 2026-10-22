package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

type forkResult struct {
	id  string
	err error
}

func (v *View) openImports() {
	v.importOpen, v.settingsOpen, v.sessionOpen, v.usageOpen = true, false, false, false
	v.agentID = v.app.Snapshot().DefaultAgentID
	v.importSelected = map[string]bool{}
	v.importQuery, v.importNotice, v.err = "", "", ""
}

func (v *View) imports(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	screenHeader(c, "Import sessions", "Add references to sessions an agent already has. History stays with the agent.", func() {
		ui.Button(c, "Manage agents").OnClick(v.openSettings)
	})
	ui.Column(c).Grow(1).MinHeight(0).Padding(16, 24).Gap(12).Children(func() {
		if len(s.Agents) == 0 {
			ui.Text(c, "Save an agent configuration before discovering its sessions.").FontSize(13).TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			choices := []choice{}
			for _, a := range s.Agents {
				choices = append(choices, choice{ID: a.ID, Name: a.Name, Description: a.Command})
			}
			ui.Column(c).Width(190).Shrink(0).Children(func() {
				selectChoice(c.Key("discovery-agent"), "Discovery agent", v.agentID, choices, s.Catalog.Loading, func(id string) { v.agentID = id; v.importSelected = map[string]bool{}; v.importNotice = "" })
			})
			ui.TextInput(c.Key("discovery-cwd"), &v.cwd).Label("Discovery working directory").Placeholder("/absolute/path/to/project").Grow(1).MinWidth(120)
			ui.PrimaryButton(c, "Find sessions").Shrink(0).Disabled(s.Catalog.Loading || v.agentID == "" || strings.TrimSpace(v.cwd) == "").OnClick(func() {
				v.importSelected = map[string]bool{}
				v.importNotice = ""
				v.fail(v.app.Discover(v.agentID, v.cwd))
			})
		})
		ui.Text(c, "The working directory only starts the agent process. Discovery lists every session the agent exposes, across directories, and adds nothing automatically.").FontSize(11.5).TextColor(t.TextMuted)
		if s.Catalog.Loading {
			ui.Row(c).Gap(8).Padding(8, 0).Children(func() {
				ui.Spinner(c).Size(14, 14)
				ui.Text(c, "Discovering sessions…").FontSize(12.5).TextColor(t.TextMuted)
			})
		}
		catalog := s.Catalog
		if catalog.Agent.ID != v.agentID {
			return
		}
		if catalog.Error != "" {
			v.errorBox(c, catalog.Error)
			return
		}
		if !catalog.Complete {
			return
		}
		available := 0
		for _, item := range catalog.Sessions {
			if item.ThreadID == "" {
				available++
			}
		}
		ui.Row(c).Gap(12).Margin(6, 0, 0, 0).Children(func() {
			ui.Text(c, fmt.Sprintf("%d sessions not added to Mu", available)).FontSize(13).FontWeight(600).SingleLine().Shrink(0)
			ui.Spacer(c)
			ui.TextInput(c.Key("session-search"), &v.importQuery).Label("Filter sessions").Placeholder("Filter sessions").Grow(1).MinWidth(120).MaxWidth(300)
		})
		if v.importNotice != "" {
			ui.Row(c).Gap(6).Children(func() {
				ui.Icon(c, checkIcon).Size(14, 14).TextColor(okColor(t))
				ui.Text(c, v.importNotice).FontSize(12).TextColor(t.TextMuted)
			})
		}
		// The list takes only the room its rows need and scrolls past that;
		// the action stays at the bottom of the screen.
		ui.Column(c).Shrink(1).MinHeight(0).Radius(8).Border(1, t.Border).Clip().Children(func() {
			ui.Scroll(c).Label("Discovered sessions").Shrink(1).MinHeight(0).Children(func() {
				shown := 0
				for _, item := range catalog.Sessions {
					if !strings.Contains(strings.ToLower(item.Title+" "+item.Cwd+" "+item.ID), strings.ToLower(v.importQuery)) {
						continue
					}
					shown++
					selected := v.importSelected[item.ID]
					added := item.ThreadID != ""
					label := item.Title
					if label == "" {
						label = item.ID
					}
					row := ui.ButtonBase(c.Key(item.ID)).Transition(colorMotion).Label("Select session: "+item.ID).Disabled(added).FillWidth().Padding(9, 14).Gap(12)
					if shown > 1 {
						row.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
					}
					switch {
					case selected:
						row.Background(t.Background.Mix(t.Accent, 0.1))
					case row.Hovered() && !added:
						row.Background(t.SurfaceHover)
					}
					dirs := strings.Join(append([]string{shortPath(item.Cwd)}, item.AdditionalDirectories...), " · ")
					row.Children(func() {
						box := ui.Box(c).Size(16, 16).Radius(4).Shrink(0).Center()
						switch {
						case added:
							box.Background(t.SurfacePressed)
						case selected:
							box.Background(t.Accent).Children(func() { ui.Icon(c, checkIcon).Size(12, 12).TextColor(t.AccentText) })
						default:
							box.Border(1.5, t.TextMuted.Alpha(0.6))
						}
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							ui.Row(c).Gap(8).Children(func() {
								title := ui.Text(c, label).FontSize(13).SingleLine().Shrink(1).MinWidth(0)
								if added {
									title.TextColor(t.TextMuted)
									chip(c, "In Mu", t.TextMuted)
								}
							})
							ui.Text(c, joinNonEmpty(" · ", dirs, sessionTime(item.UpdatedAt), shortID(item.ID))).SingleLine().Tooltip(strings.Join(append([]string{item.Cwd}, item.AdditionalDirectories...), "\n") + "\n" + item.ID).FontSize(11.5).TextColor(t.TextMuted)
						})
					}).OnClick(func() { v.importSelected[item.ID] = !v.importSelected[item.ID] })
				}
				if shown == 0 {
					ui.Text(c, "No matching sessions.").Padding(12, 14).FontSize(12.5).TextColor(t.TextMuted)
				}
			})
		})
		selected := []string{}
		for _, item := range catalog.Sessions {
			if v.importSelected[item.ID] && item.ThreadID == "" {
				selected = append(selected, item.ID)
			}
		}
		ui.Spacer(c)
		ui.Row(c).Gap(10).Shrink(0).Children(func() {
			text := "Select sessions to add."
			if len(selected) > 0 {
				text = fmt.Sprintf("%d selected", len(selected))
			}
			ui.Text(c, text).FontSize(12).TextColor(t.TextMuted).Grow(1)
			ui.PrimaryButton(c, "Add selected to Mu").Disabled(len(selected) == 0).OnClick(func() {
				ids, err := v.app.Import(catalog.Generation, selected)
				v.fail(err)
				if err == nil {
					v.importSelected = map[string]bool{}
					v.importNotice = fmt.Sprintf("Added %d session references. Open a thread to load its history.", len(ids))
				}
			})
		})
	})
}

func sessionTime(value string) string {
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return timeLabel(at)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}
