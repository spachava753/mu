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
	app                          *app.App
	selected                     string
	creating, archived, deleting bool
	deleteID                     string
	command, cwd                 string
	err                          string
	drafts                       map[string]*string
	lists                        map[string]*ui.ListState
	expanded                     map[string]*bool
	md                           markdown.Options
}

func New(a *app.App, command, cwd string) *View {
	s := a.Snapshot()
	if command == "" {
		command = s.LastCommand
	}
	if cwd == "" {
		cwd = s.LastCwd
	}
	v := &View{app: a, command: command, cwd: cwd, drafts: map[string]*string{}, lists: map[string]*ui.ListState{}, expanded: map[string]*bool{}}
	for _, t := range s.Threads {
		if !t.Archived {
			v.selected = t.ID
			break
		}
	}
	v.creating = v.selected == ""
	return v
}

func (v *View) selectThread(id string) { v.selected, v.creating, v.err = id, false, "" }
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
func (v *View) send(id, text string) bool {
	if err := v.app.Send(id, text); err != nil {
		v.fail(err)
		return false
	}
	*v.draft(id) = ""
	v.err = ""
	if list := v.lists[id]; list != nil {
		list.ScrollToEnd()
	}
	return true
}

func (v *View) Draw(c *ui.Context) {
	s := v.app.Snapshot()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		v.sidebar(c, s)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			if s.StorageError != "" {
				v.errorBox(c, s.StorageError)
			}
			if v.err != "" {
				v.errorBox(c, v.err)
			}
			if v.creating {
				v.createForm(c)
				return
			}
			for _, t := range s.Threads {
				if t.ID == v.selected {
					v.thread(c, t)
					return
				}
			}
			ui.Column(c).Fill().Center().Gap(12).Children(func() {
				ui.Text(c, "No thread selected").FontSize(22).Bold()
				ui.Text(c, "Choose a thread or create a new one.").TextColor(c.Theme().TextMuted)
			})
		})
	})
	ui.Modal(c, &v.deleting, func() {
		ui.Text(c, "Delete this thread?").FontSize(20).Bold()
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
	})
}

func (v *View) sidebar(c *ui.Context, s app.Snapshot) {
	ui.Column(c).Width(248).Shrink(0).Background(c.Theme().Surface).BorderWidth(0, 1, 0, 0).BorderColor(c.Theme().Border).Children(func() {
		ui.Row(c).Padding(16).Children(func() {
			ui.Text(c, "mu").FontSize(26).Bold().Grow(1)
			ui.Text(c, "ACP client").FontSize(12).TextColor(c.Theme().TextMuted)
		})
		ui.PrimaryButton(c, "New thread").Margin(0, 12, 12, 12).OnClick(func() { v.creating = true; v.err = "" })
		label := "Threads"
		if v.archived {
			label = "Archived threads"
		}
		ui.Text(c, label).Bold().Padding(4, 16, 8, 16)
		ui.Scroll(c).Grow(1).Gap(4).Padding(0, 8).Children(func() {
			count := 0
			for _, t := range s.Threads {
				if t.Archived != v.archived {
					continue
				}
				count++
				button := ui.ButtonBase(c.Key(t.ID)).Label(t.Title).Padding(10, 8).Radius(6).FillWidth()
				if t.ID == v.selected && !v.creating {
					button.Background(c.Theme().Accent.Alpha(0.13))
				}
				button.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, t.Title).SingleLine()
						status := t.Updated.Local().Format("Jan 2, 15:04")
						if t.Running {
							status = t.Status
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
				ui.Text(c, "No threads here.").Padding(8).TextColor(c.Theme().TextMuted)
			}
		})
		ui.Button(c, map[bool]string{false: "Show archived", true: "Show active"}[v.archived]).Margin(12).OnClick(func() { v.archived = !v.archived })
	})
}

func (v *View) createForm(c *ui.Context) {
	ui.Scroll(c).Grow(1).Padding(32).Children(func() {
		ui.Column(c).MaxWidth(640).Gap(14).Children(func() {
			ui.Text(c, "New thread").FontSize(26).Bold()
			ui.Text(c, "Run an ACP agent in a working directory.").TextColor(c.Theme().TextMuted)
			ui.Text(c, "Agent command").Bold().Margin(12, 0, 0, 0)
			ui.TextInput(c.Key("agent-command"), &v.command).Label("Agent command").Placeholder("Executable and arguments").AutoFocus()
			ui.Text(c, "Executable and arguments, with quotes for spaces. No shell expansion.").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Text(c, "Working directory").Bold()
			ui.TextInput(c.Key("working-directory"), &v.cwd).Label("Working directory").Placeholder("/path/to/project")
			ui.Text(c, "Use an agent you trust. It runs as your user, with your environment and filesystem access. Sign in with the agent’s own CLI first.").TextColor(c.Theme().TextMuted)
			ui.Row(c).Gap(8).Margin(8, 0).Children(func() {
				ui.PrimaryButton(c, "Create thread").Disabled(strings.TrimSpace(v.command) == "" || strings.TrimSpace(v.cwd) == "").OnClick(func() {
					id, err := v.app.Create(v.command, v.cwd)
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
		ui.Column(c).Padding(16, 24).Gap(6).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			ui.Text(c, t.Title).FontSize(20).Bold().SingleLine()
			ui.Text(c, t.Cwd).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(t.Cwd)
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, t.Command).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().Grow(1).MinWidth(0).Tooltip(t.Command)
				ui.Button(c, "Fork").Disabled(t.Running).Tooltip("Start an independent thread with this transcript as context").OnClick(func() {
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
				ui.Button(c, archive).Disabled(t.Running).OnClick(func() {
					if err := v.app.Archive(t.ID, !t.Archived); err != nil {
						v.fail(err)
						return
					}
					v.archived = !t.Archived
				})
				ui.Button(c, "Delete").Disabled(t.Running).OnClick(func() { v.deleteID = t.ID; v.deleting = true })
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
		ui.Column(c).Padding(12, 24, 20, 24).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
			if t.Archived {
				ui.Text(c, "Archived. Restore this thread to continue.").TextColor(c.Theme().TextMuted)
				return
			}
			ui.TextArea(c.Key("message"), v.draft(t.ID)).Label("Message").Placeholder("Message the agent…").Lines(2, 6).Disabled(t.Running).OnTextKey(func(k ui.TextKey) bool {
				if k.Key == ui.KeyEnter && k.Mods == 0 && !t.Running {
					if v.send(t.ID, k.Text()) {
						k.SetText("")
					}
					return true
				}
				return false
			})
			ui.Row(c).Gap(8).Children(func() {
				status := "Enter to send · Shift+Enter for a new line"
				if t.Running {
					status = t.Status
				}
				ui.Text(c, status).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
				if t.Running {
					ui.Button(c, "Stop").OnClick(func() { v.app.Stop(t.ID) })
				} else {
					ui.PrimaryButton(c, "Send").Disabled(strings.TrimSpace(*v.draft(t.ID)) == "").OnClick(func() { v.send(t.ID, *v.draft(t.ID)) })
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
			key := threadID + e.ID
			if v.expanded[key] == nil {
				v.expanded[key] = new(bool)
			}
			ui.Collapsible(c, label, v.expanded[key], func() { ui.Text(c, e.Text).Selectable().FontSize(12) })
		default:
			ui.Text(c, e.Text).Selectable().FontSize(12).TextColor(c.Theme().TextMuted)
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
	ui.Scroll(c).MaxHeight(100).Padding(10, 24).Background(c.Theme().Danger.Alpha(0.08)).Children(func() { ui.Text(c, text).TextColor(c.Theme().Danger).Selectable().FontSize(12) })
}
