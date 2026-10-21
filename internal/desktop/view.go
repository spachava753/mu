// Package desktop is the Go-only MyGo interface. It contains no webview or frontend runtime.
package desktop

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/state"
)

type View struct {
	app                                  *app.App
	selected                             string
	creating, archived, deleting         bool
	settingsOpen, sessionOpen, usageOpen bool
	picker                               commandPicker
	deleteID                             string
	command, cwd, agentID                string
	customCommand                        bool
	editAgent                            state.AgentConfig
	makeDefault                          bool
	settingsNotice                       string
	removeAgent                          bool
	err                                  string
	drafts                               map[string]*string
	lists                                map[string]*ui.ListState
	expanded                             map[string]*bool
	md                                   markdown.Options
}

func New(a *app.App, command, cwd string) *View {
	s := a.Snapshot()
	custom := command != "" || len(s.Agents) == 0
	if command == "" {
		command = s.LastCommand
	}
	if cwd == "" {
		cwd = s.LastCwd
	}
	v := &View{app: a, command: command, cwd: cwd, agentID: s.DefaultAgentID, customCommand: custom, drafts: map[string]*string{}, lists: map[string]*ui.ListState{}, expanded: map[string]*bool{}}
	for _, t := range s.Threads {
		if !t.Archived {
			v.selected = t.ID
			break
		}
	}
	v.creating = v.selected == ""
	return v
}
func (v *View) selectThread(id string) {
	v.picker, v.usageOpen = commandPicker{}, false
	v.selected, v.creating, v.settingsOpen, v.sessionOpen, v.err = id, false, false, false, ""
}
func (v *View) openNew() {
	v.picker, v.usageOpen = commandPicker{}, false
	v.creating, v.settingsOpen, v.sessionOpen, v.err = true, false, false, ""
	v.agentID = v.app.Snapshot().DefaultAgentID
}
func (v *View) fail(err error) {
	if err != nil {
		v.err = err.Error()
	} else {
		v.err = ""
	}
}
func (v *View) draft(id string) *string {
	if v.drafts[id] == nil {
		v.drafts[id] = new(string)
	}
	return v.drafts[id]
}
func (v *View) disclosure(key string) *bool {
	if v.expanded[key] == nil {
		v.expanded[key] = new(bool)
	}
	return v.expanded[key]
}
func (v *View) send(id, text string) bool {
	if err := v.app.Send(id, text); err != nil {
		v.fail(err)
		return false
	}
	*v.draft(id), v.err = "", ""
	if list := v.lists[id]; list != nil {
		list.ScrollToEnd()
	}
	return true
}

func (v *View) Draw(c *ui.Context) {
	matte(c)
	// Keep the overlay parent alive so first-open and last-close transitions
	// can animate. An empty layer draws nothing and takes no input.
	ui.Overlay(c, func() {})
	s := v.app.Snapshot()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		v.sidebar(c, s)
		ui.Column(c).Grow(1).MinWidth(0).Background(c.Theme().Background).Children(func() {
			if s.StorageError != "" {
				v.errorBox(c, s.StorageError)
			}
			if v.err != "" {
				v.errorBox(c, v.err)
			}
			screen := v.selected
			if v.creating {
				screen = "new"
			}
			if v.settingsOpen {
				screen = "settings"
			}
			ui.Column(c.Key("screen-" + screen)).Grow(1).MinHeight(0).MinWidth(0).Transition(fadeMotion).Children(func() {
				if v.settingsOpen {
					v.settings(c, s)
					return
				}
				if v.creating {
					v.createForm(c, s)
					return
				}
				for _, t := range s.Threads {
					if t.ID == v.selected {
						v.thread(c, t)
						return
					}
				}
				ui.Column(c).Fill().Center().Gap(10).Children(func() {
					ui.Text(c, "No thread selected").FontSize(20).Bold()
					ui.Text(c, "Choose a thread or create a new one.").TextColor(c.Theme().TextMuted)
				})
			})
		})
	})
	modal(c, &v.deleting, func() {
		ui.Text(c, "Delete this thread?").FontSize(18).Bold()
		ui.Text(c, "This permanently removes the transcript from Mu. It does not delete files or the agent’s own session history.").MaxWidth(420)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "Keep thread").OnClick(func() { v.deleting = false })
			ui.Button(c, "Delete permanently").TextColor(c.Theme().Danger).OnClick(func() {
				if err := v.app.Delete(v.deleteID); err != nil {
					v.fail(err)
					v.deleting = false
					return
				}
				if v.selected == v.deleteID {
					v.selected = ""
				}
				delete(v.drafts, v.deleteID)
				delete(v.lists, v.deleteID)
				v.deleting = false
			})
		})
	}).Border(1, c.Theme().Border).Shadow(0, 0, 0, 0, ui.Transparent)
	v.sessionDialog(c, s)
	v.removeAgentDialog(c)
}

func (v *View) sidebar(c *ui.Context, s app.Snapshot) {
	target := float32(248)
	if s.SidebarCollapsed {
		target = 48
	}
	sidebar := ui.Column(c.Key("sidebar")).Label("Thread sidebar").Shrink(0).ClipX().Background(c.Theme().Surface).BorderWidth(0, 1, 0, 0).BorderColor(c.Theme().Border)
	width := sidebar.AnimateTo("width", target, sidebarMotion)
	sidebar.Width(width).Children(func() {
		ui.Row(c).Height(56).Padding(8).Children(func() {
			ui.Text(c, "mu").FontSize(21).Bold().Padding(0, 6).Grow(1).MinWidth(0).SingleLine().Opacity(max(0, min(1, (width-48)/200)))
			label := "Collapse sidebar"
			if s.SidebarCollapsed {
				label = "Expand sidebar"
			}
			iconButton(c, label, panelIcon).OnClick(func() { v.fail(v.app.SetSidebarCollapsed(!s.SidebarCollapsed)) })
		})
		if s.SidebarCollapsed {
			ui.Column(c.Key("rail")).Width(47).Grow(1).Transition(fadeMotion).Children(func() {
				iconButton(c, "New thread", plusIcon).Margin(4, 8).OnClick(v.openNew)
				ui.Spacer(c)
				iconButton(c, "Settings", settingsIcon).Margin(4, 8, 12, 8).OnClick(v.openSettings)
			})
			return
		}
		ui.Column(c.Key("thread-list")).Width(247).Grow(1).MinHeight(0).Transition(fadeMotion).Children(func() {
			ui.Button(c, "New thread").Margin(0, 12, 18, 12).OnClick(v.openNew)
			label := "Threads"
			if v.archived {
				label = "Archived threads"
			}
			ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted).Padding(0, 18, 8, 18)
			ui.Scroll(c).Grow(1).Gap(3).Padding(0, 8).Children(func() {
				count := 0
				for _, t := range s.Threads {
					if t.Archived != v.archived {
						continue
					}
					count++
					selected := t.ID == v.selected && !v.creating && !v.settingsOpen
					button := ui.ButtonBase(c.Key(t.ID)).Transition(colorMotion).Label("Thread: "+t.Title).Padding(10, 8).Radius(5).FillWidth().BorderWidth(0, 0, 0, 2)
					button.Background(rowColor(c.Theme(), selected, button.Hovered()))
					if selected {
						button.BorderColor(c.Theme().TextMuted)
					} else {
						button.BorderColor(ui.Transparent)
					}
					button.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Text(c, t.Title).SingleLine().FontSize(13)
							status := t.Updated.Local().Format("Jan 2, 15:04")
							if t.Running {
								status = t.Status
							}
							if t.Configuring {
								status = "Updating session…"
							}
							if len(t.Permissions) > 0 {
								status = "Permission needed"
							}
							if t.Error != "" {
								status = "Agent error"
							}
							ui.Text(c, status).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
						})
					}).OnClick(func() { v.selectThread(t.ID) })
				}
				if count == 0 {
					ui.Text(c, "No threads here.").Padding(8).FontSize(12).TextColor(c.Theme().TextMuted)
				}
			})
			ui.Row(c).Padding(10, 12).Gap(6).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
				label := "Show archived"
				if v.archived {
					label = "Show active"
				}
				ui.Button(c, label).Grow(1).OnClick(func() { v.archived = !v.archived })
				iconButton(c, "Settings", settingsIcon).OnClick(v.openSettings)
			})
		})
	})
}

func (v *View) createForm(c *ui.Context, s app.Snapshot) {
	ui.Scroll(c).Grow(1).Padding(28).Children(func() {
		ui.Column(c).MaxWidth(600).Gap(14).Children(func() {
			ui.Text(c, "New thread").FontSize(22).Bold()
			ui.Text(c, "Choose an agent and a working directory.").TextColor(c.Theme().TextMuted)
			if len(s.Agents) > 0 && !v.customCommand {
				ui.Text(c, "Agent").Bold().Margin(12, 0, 0, 0)
				choices := []choice{}
				for _, a := range s.Agents {
					choices = append(choices, choice{ID: a.ID, Name: a.Name, Description: a.Command})
				}
				selectChoice(c.Key("agent"), "Configured agent", v.agentID, choices, false, func(id string) { v.agentID = id })
				for _, a := range s.Agents {
					if a.ID == v.agentID {
						ui.Text(c, a.Command).SingleLine().FontSize(12).TextColor(c.Theme().TextMuted).Tooltip(a.Command)
					}
				}
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "Manage agents").OnClick(v.openSettings)
					ui.Button(c, "Use custom command").OnClick(func() { v.customCommand = true })
				})
			} else {
				ui.Text(c, "Agent command").Bold().Margin(12, 0, 0, 0)
				ui.TextInput(c.Key("agent-command"), &v.command).Label("Agent command").Placeholder("Executable and arguments").AutoFocus()
				ui.Text(c, "Executable and arguments, with quotes for spaces. No shell expansion.").FontSize(12).TextColor(c.Theme().TextMuted)
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "Save agents in Settings").OnClick(v.openSettings)
					if len(s.Agents) > 0 {
						ui.Button(c, "Use saved agent").OnClick(func() { v.customCommand = false })
					}
				})
			}
			ui.Text(c, "Working directory").Bold()
			ui.TextInput(c.Key("working-directory"), &v.cwd).Label("Working directory").Placeholder("/path/to/project")
			ui.Text(c, "Agents run as your user, with your environment and filesystem access. Use commands you trust.").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Row(c).Gap(8).Margin(8, 0).Children(func() {
				custom := v.customCommand || len(s.Agents) == 0
				missing := (custom && strings.TrimSpace(v.command) == "") || (!custom && v.agentID == "") || strings.TrimSpace(v.cwd) == ""
				ui.PrimaryButton(c, "Create thread").Disabled(missing).OnClick(func() {
					var id string
					var err error
					if custom {
						id, err = v.app.Create(v.command, v.cwd)
					} else {
						id, err = v.app.CreateWithAgent(v.agentID, v.cwd)
					}
					if err != nil {
						v.fail(err)
						return
					}
					v.archived = false
					v.selectThread(id)
				})
				if v.selected != "" {
					ui.Button(c, "Cancel").OnClick(func() { v.creating = false; v.err = "" })
				}
			})
		})
	})
}

func (v *View) thread(c *ui.Context, t app.ThreadView) {
	ui.Column(c.Key(t.ID)).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(16, 24).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			ui.Text(c, t.Title).FontSize(18).Bold().SingleLine()
			ui.Text(c, t.Cwd).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(t.Cwd)
			ui.Row(c).Gap(6).Wrap().Children(func() {
				name := t.AgentName
				if name == "" {
					name = t.Command
				}
				ui.Text(c, name).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().Grow(1).MinWidth(0).Tooltip(t.Command)
				ui.Button(c, "Session options").OnClick(func() {
					v.sessionOpen = true
					if !t.Archived {
						v.fail(v.app.Connect(t.ID))
					}
				})
				busy := t.Running || t.Configuring
				ui.Button(c, "Fork").Disabled(busy).Tooltip("Start an independent thread with this transcript as context").OnClick(func() {
					id, err := v.app.Fork(t.ID)
					if err != nil {
						v.fail(err)
						return
					}
					v.archived = false
					v.selectThread(id)
				})
				archive := "Archive"
				if t.Archived {
					archive = "Restore"
				}
				ui.Button(c, archive).Disabled(busy).OnClick(func() {
					if err := v.app.Archive(t.ID, !t.Archived); err != nil {
						v.fail(err)
						return
					}
					v.archived = !t.Archived
				})
				ui.Button(c, "Delete").Disabled(busy).OnClick(func() { v.deleteID = t.ID; v.deleting = true })
			})
		})
		if t.Notice != "" {
			ui.Text(c, t.Notice).Padding(10, 24).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		list := v.lists[t.ID]
		if list == nil {
			list = &ui.ListState{FollowEnd: true}
			v.lists[t.ID] = list
		}
		list.Key = func(i int) any { return t.Entries[i].ID }
		ui.List(c.Key("messages"), list, len(t.Entries), func(i int) { v.entry(c, t.ID, t.Entries[i]) }).Grow(1).MinHeight(0).Padding(20, 24).Gap(20).Children(func() {
			if len(t.Entries) == 0 {
				ui.Text(c, "Send a message to start the agent.").Padding(12, 0).TextColor(c.Theme().TextMuted)
			}
		})
		if !list.AtEnd() && len(t.Entries) > 0 {
			ui.Button(c, "Jump to latest").AlignSelf(ui.Center).OnClick(list.ScrollToEnd)
		}
		for _, p := range t.Permissions {
			v.permission(c, t.ID, p)
		}
		if t.Error != "" {
			v.errorBox(c, t.Error)
		}
		ui.Column(c).Padding(12, 24, 16, 24).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
			if t.Archived {
				v.usageLine(c, t.Session)
				ui.Text(c, "Archived. Restore this thread to continue.").TextColor(c.Theme().TextMuted)
				return
			}
			v.messageInput(c, t)
			v.usageLine(c, t.Session)
			ui.Row(c).Gap(8).Children(func() {
				if t.Running || t.Configuring {
					ui.Spinner(c).Size(14, 14).Label("Agent working")
				}
				status := "Enter to send · Shift+Enter for a new line"
				if t.Running {
					status = t.Status
				} else if t.Configuring {
					status = "Updating session…"
				}
				ui.Text(c, status).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
				if t.Running {
					ui.Button(c, "Stop").OnClick(func() { v.app.Stop(t.ID) })
				} else {
					ui.PrimaryButton(c, "Send").Disabled(t.Configuring || strings.TrimSpace(*v.draft(t.ID)) == "").OnClick(func() { v.send(t.ID, *v.draft(t.ID)) })
				}
			})
		})
	})
}

func (v *View) entry(c *ui.Context, threadID string, e state.Entry) {
	ui.Column(c).Gap(6).Children(func() {
		switch e.Role {
		case "assistant":
			ui.Text(c, "Agent").Bold().FontSize(12).TextColor(c.Theme().TextMuted)
			markdown.View(c, e.Text, &v.md)
		case "user":
			ui.Text(c, "You").Bold().FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Text(c, e.Text).Selectable()
		case "tool", "thought", "plan":
			label := e.Title
			if e.Role == "thought" {
				label = "Thinking"
			}
			if e.Role == "plan" {
				label = "Plan"
			}
			if e.Status != "" {
				label += " — " + e.Status
			}
			ui.Collapsible(c, label, v.disclosure(threadID+e.ID), func() {
				if e.Role == "plan" {
					markdown.View(c, e.Text, &v.md)
				} else {
					ui.Text(c, e.Text).Selectable().FontSize(12)
				}
				if e.Details != "" {
					ui.Collapsible(c, "Tool / protocol details", v.disclosure("details"+threadID+e.ID), func() { ui.Text(c, e.Details).Selectable().Font("monospace").FontSize(11) })
				}
			})
		default:
			ui.Text(c, e.Text).Selectable().FontSize(12).TextColor(c.Theme().TextMuted)
		}
		if e.Details != "" && (e.Role == "user" || e.Role == "assistant") {
			ui.Collapsible(c, "Content details", v.disclosure("content"+threadID+e.ID), func() { ui.Text(c, e.Details).Selectable().FontSize(11) })
		}
	})
}

func (v *View) permission(c *ui.Context, id string, p app.Permission) {
	ui.Column(c.Key(p.ID)).Padding(12, 24).Gap(8).Background(c.Theme().Surface).Children(func() {
		title := "Permission needed"
		if p.Request.ToolCall.Title != nil {
			title += " for " + *p.Request.ToolCall.Title
		}
		ui.Text(c, title).Bold()
		if p.Request.ToolCall.RawInput != nil || len(p.Request.ToolCall.Content) > 0 || len(p.Request.ToolCall.Locations) > 0 {
			b, _ := json.MarshalIndent(p.Request.ToolCall, "", "  ")
			ui.Scroll(c).MaxHeight(120).Children(func() { ui.Text(c, string(b)).Selectable().FontSize(12) })
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, o := range p.Request.Options {
				ui.Button(c.Key(string(o.OptionId)), o.Name).OnClick(func() { v.app.Answer(id, p.ID, o.OptionId) })
			}
			if len(p.Request.Options) == 0 {
				ui.Text(c, "The agent offered no options. Stop the turn to cancel.")
			}
		})
	})
}
func (v *View) errorBox(c *ui.Context, text string) {
	ui.Scroll(c).MaxHeight(100).Padding(10, 24).Background(c.Theme().Background.Mix(c.Theme().Danger, 0.08)).Children(func() { ui.Text(c, text).TextColor(c.Theme().Danger).Selectable().FontSize(12) })
}
