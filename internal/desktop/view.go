// Package desktop is the Go-only MyGo interface. It contains no webview or frontend runtime.
package desktop

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/state"
)

type View struct {
	app                                  *app.App
	selected                             string
	importOpen                           bool
	importSelected                       map[string]bool
	importQuery, importNotice            string
	forkResult                           chan forkResult
	creating, archived, deleting         bool
	settingsOpen, sessionOpen, usageOpen bool
	picker                               commandPicker
	deleteID                             string
	renaming, renameFresh                bool
	renameID, renameTitle, renameError   string
	command, agentID, workDir, projectID string
	editProject                          state.Project
	projectNotice                        string
	projectForm                          bool
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

func New(a *app.App, command string) *View {
	s := a.Snapshot()
	custom := command != "" || len(s.Agents) == 0
	if command == "" {
		command = s.LastCommand
	}
	v := &View{app: a, command: command, agentID: s.DefaultAgentID, customCommand: custom, drafts: map[string]*string{}, lists: map[string]*ui.ListState{}, expanded: map[string]*bool{}}
	for _, t := range s.Threads {
		if !t.Archived {
			v.selected = t.ID
			break
		}
	}
	v.creating = v.selected == ""
	if v.selected != "" {
		v.loadSelected()
	}
	return v
}
func (v *View) selectThread(id string) {
	v.importOpen = false
	v.picker, v.usageOpen = commandPicker{}, false
	v.selected, v.creating, v.settingsOpen, v.sessionOpen, v.err = id, false, false, false, ""
	v.loadSelected()
}

func (v *View) loadSelected() {
	for _, t := range v.app.Snapshot().Threads {
		if t.ID == v.selected && t.SessionID != "" && !t.Archived {
			v.fail(v.app.Connect(t.ID))
			break
		}
	}
}
func (v *View) openNew() {
	v.importOpen = false
	v.picker, v.usageOpen = commandPicker{}, false
	v.creating, v.settingsOpen, v.sessionOpen, v.err = true, false, false, ""
	s := v.app.Snapshot()
	v.agentID, v.projectID = s.DefaultAgentID, ""
	// Start in the project of the thread being viewed.
	for _, t := range s.Threads {
		if t.ID == v.selected {
			v.projectID = t.ProjectID
		}
	}
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

func (v *View) archive(t app.ThreadView) {
	if err := v.app.Archive(t.ID, !t.Archived); err != nil {
		v.fail(err)
		return
	}
	if t.ID == v.selected {
		v.archived = !t.Archived
		if t.Archived {
			v.loadSelected()
		}
	}
}
func (v *View) confirmRemove(id string) { v.deleteID, v.deleting = id, true }
func (v *View) fork(id string) {
	result := make(chan forkResult, 1)
	v.forkResult = result
	go func() { id, err := v.app.Fork(id); result <- forkResult{id, err} }()
}
func (v *View) openSessionOptions(t app.ThreadView) {
	v.sessionOpen = true
	if !t.Archived {
		v.fail(v.app.Connect(t.ID))
	}
}

func (v *View) Draw(c *ui.Context) {
	if v.forkResult != nil {
		select {
		case result := <-v.forkResult:
			v.forkResult = nil
			// A saved fork opens even if releasing it from the source failed.
			if result.id != "" {
				v.archived = false
				v.selectThread(result.id)
			}
			v.fail(result.err)
		default:
		}
	}
	matte(c)
	v.md.Theme = markdownTheme(c.Theme())
	// Keep the overlay parent alive so first-open and last-close transitions
	// can animate. An empty layer draws nothing and takes no input.
	ui.Overlay(c, func() {})
	s := v.app.Snapshot()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		v.sidebar(c, s)
		// The content is an inset panel on the chrome, as in Linear.
		ui.Column(c).Grow(1).MinWidth(0).Margin(8, 8, 8, 0).Radius(8).Border(1, c.Theme().Border).Background(c.Theme().Background).Clip().Children(func() {
			if s.LifecycleError != "" {
				v.errorBox(c, s.LifecycleError)
			}
			if s.StorageError != "" {
				v.errorBox(c, s.StorageError)
			}
			if v.err != "" {
				v.dismissibleError(c, v.err)
			}
			screen := v.selected
			if v.creating {
				screen = "new"
			}
			if v.settingsOpen {
				screen = "settings"
			}
			if v.importOpen {
				screen = "imports"
			}
			ui.Column(c.Key("screen-" + screen)).Grow(1).MinHeight(0).MinWidth(0).Transition(fadeMotion).Children(func() {
				if v.importOpen {
					v.imports(c, s)
					return
				}
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
				ui.Column(c).Fill().Center().Gap(6).Padding(24).Children(func() {
					ui.Text(c, "No thread selected").FontSize(14).FontWeight(500)
					ui.Text(c, "Choose a thread from the sidebar, or start a new one.").FontSize(13).TextColor(c.Theme().TextMuted)
					ui.PrimaryButton(c, "Start a thread").Margin(8, 0, 0, 0).OnClick(v.openNew)
				})
			})
		})
	})
	modal(c, &v.deleting, func() {
		ui.Text(c, "Remove this thread from Mu?").FontSize(14).FontWeight(500)
		ui.Text(c, "This removes only Mu’s session reference. The agent’s history and workspace files are unchanged. You can import the session again.").FontSize(13).TextColor(c.Theme().TextMuted).MaxWidth(420)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			ui.Button(c, "Keep thread").OnClick(func() { v.deleting = false })
			ui.Button(c, "Remove reference").TextColor(c.Theme().Danger).OnClick(func() {
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
	v.sessionDialog(c, s)
	v.renameDialog(c)
	v.removeAgentDialog(c)
}

func (v *View) sidebar(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	target := float32(sidebarWidth)
	if s.SidebarCollapsed {
		target = railWidth
	}
	sidebar := ui.Column(c.Key("sidebar")).Label("Thread sidebar").Shrink(0).ClipX()
	width := sidebar.AnimateTo("width", target, sidebarMotion)
	sidebar.Width(width).Children(func() {
		toggle := func() { v.fail(v.app.SetSidebarCollapsed(!s.SidebarCollapsed)) }
		if s.SidebarCollapsed {
			ui.Column(c.Key("rail")).Width(railWidth-1).Grow(1).AlignItems(ui.Center).Padding(10, 0).Gap(4).Transition(fadeMotion).Children(func() {
				iconButton(c, "Expand sidebar", panelIcon).OnClick(toggle)
				ui.Box(c).Height(8)
				iconButton(c, "New thread", plusIcon).OnClick(v.openNew)
				iconButton(c, "Import sessions", importIcon).OnClick(v.openImports)
				ui.Spacer(c)
				iconButton(c, "Settings", gearIcon).OnClick(v.openSettings)
			})
			return
		}
		ui.Column(c.Key("thread-list")).Width(sidebarWidth - 1).Grow(1).MinHeight(0).Transition(fadeMotion).Children(func() {
			ui.Row(c).Height(52).Shrink(0).Padding(0, 10, 0, 18).Gap(2).Children(func() {
				ui.Text(c, "mu").FontSize(14).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
				iconButton(c, "New thread", plusIcon).OnClick(v.openNew)
				iconButton(c, "Collapse sidebar", panelIcon).OnClick(toggle)
			})
			count := 0
			for _, thread := range s.Threads {
				if thread.Archived == v.archived {
					count++
				}
			}
			ui.Row(c).Shrink(0).Padding(6, 10, 4, 18).Gap(6).Children(func() {
				label := "Threads"
				if v.archived {
					label = "Archived"
				}
				ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(faint(t)).SingleLine()
				ui.Text(c, itoa(count)).FontSize(12).TextColor(faint(t)).Grow(1)
				toggle := "Show archived"
				if v.archived {
					toggle = "Show active"
				}
				quietButton(c, toggle).OnClick(func() { v.archived = !v.archived })
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(1).Padding(2, 10, 8, 10).Children(func() {
				v.threadGroups(c, s)
				if count == 0 {
					text := "No threads yet."
					if v.archived {
						text = "No archived threads."
					}
					ui.Text(c, text).Padding(8).FontSize(12).TextColor(t.TextMuted)
				}
			})
			ui.Column(c).Shrink(0).Padding(6, 10, 10, 10).Gap(1).Children(func() {
				navRow(c, "Import sessions", importIcon, v.importOpen).OnClick(v.openImports)
				navRow(c, "Settings", gearIcon, v.settingsOpen).OnClick(v.openSettings)
			})
		})
	})
}

// threadGroups lists the visible threads, grouped by project once any
// project exists. Threads without a project come last.
func (v *View) threadGroups(c *ui.Context, s app.Snapshot) {
	visible := func(thread app.ThreadView) bool { return thread.Archived == v.archived }
	group := func(id, name string) {
		header := false
		for _, thread := range s.Threads {
			if !visible(thread) || thread.ProjectID != id {
				continue
			}
			if !header && len(s.Projects) > 0 {
				header = true
				ui.Text(c.Key("group-"+id), name).Padding(10, 8, 3, 8).FontSize(11.5).FontWeight(500).TextColor(faint(c.Theme())).SingleLine()
			}
			v.threadRow(c, s, thread)
		}
	}
	known := map[string]bool{}
	for _, p := range s.Projects {
		known[p.ID] = true
		group(p.ID, p.Name)
	}
	group("", "No project")
	for _, thread := range s.Threads {
		if visible(thread) && thread.ProjectID != "" && !known[thread.ProjectID] {
			v.threadRow(c, s, thread) // Not expected: removal clears the project.
		}
	}
}

// threadStatus is the most important state of a thread, if any, and the
// color that marks it.
func threadStatus(t *ui.Theme, thread app.ThreadView) (string, ui.Color) {
	switch {
	case thread.Error != "":
		return "Agent error", t.Danger
	case len(thread.Permissions) > 0:
		return "Needs permission", warnColor(t)
	case thread.Configuring:
		return "Loading session…", t.TextMuted
	case thread.Running:
		status := thread.Status
		if status == "" {
			status = "Working…"
		}
		return status, t.Accent
	case thread.TitleError != "":
		return "Title not stored", warnColor(t)
	}
	return "", ui.Transparent
}

func (v *View) threadRow(c *ui.Context, s app.Snapshot, thread app.ThreadView) {
	t := c.Theme()
	selected := thread.ID == v.selected && !v.creating && !v.settingsOpen && !v.importOpen
	button := ui.ButtonBase(c.Key(thread.ID)).Transition(colorMotion).Label("Thread: "+thread.Title).Padding(6, 8).Radius(5).FillWidth()
	button.Background(sideColor(t, selected, button.Hovered()))
	busy := thread.Running || thread.Configuring
	button.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			status, color := threadStatus(t, thread)
			ui.Row(c).Gap(6).Children(func() {
				title := ui.Text(c, thread.Title).FontSize(13).FontWeight(500).SingleLine().Grow(1).MinWidth(0)
				if !selected {
					title.TextColor(t.Text.Mix(t.TextMuted, 0.35))
				}
				if status != "" && !thread.Configuring {
					dot(c, color)
				}
			})
			meta := status
			if meta == "" {
				agent := thread.AgentName
				if agent == "" {
					agent = commandName(thread.Command)
				}
				// Project headings and Mu's own randomly named folders say
				// nothing a row needs; other folders identify the work.
				folder := filepath.Base(thread.Cwd)
				if thread.ProjectID != "" || filepath.Dir(thread.Cwd) == s.WorkingDirectory {
					folder = ""
				}
				meta = joinNonEmpty(" · ", agent, folder, timeLabel(thread.Updated))
			}
			text := ui.Text(c, meta).FontSize(11.5).TextColor(faint(t)).SingleLine()
			if status != "" && color != t.TextMuted {
				text.TextColor(color)
			}
		})
	}).OnClick(func() { v.selectThread(thread.ID) }).ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename…").Chosen() {
			v.openRename(thread)
		}
		label := "Archive"
		if thread.Archived {
			label = "Restore"
		}
		if m.Item(label).Disabled(busy).Chosen() {
			v.archive(thread)
		}
		m.Separator()
		if m.Item("Remove from Mu…").Disabled(busy).Chosen() {
			v.confirmRemove(thread.ID)
		}
	})
}

// commandName is the executable of a command line, for compact labels.
func commandName(command string) string {
	fields := strings.Fields(strings.Trim(command, `"'`))
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(strings.Trim(fields[0], `"'`))
}

func itoa(n int) string { return count(uint64(max(n, 0))) }

func (v *View) createForm(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	screenHeader(c, "New thread", "Start an agent session in a new folder.", func() {
		if v.selected != "" {
			ui.Button(c, "Cancel").OnClick(func() { v.creating = false; v.err = "" })
		}
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(20, 24).Children(func() {
		ui.Column(c).MaxWidth(560).Gap(18).Children(func() {
			if len(s.Agents) > 0 && !v.customCommand {
				var command string
				for _, a := range s.Agents {
					if a.ID == v.agentID {
						command = a.Command
					}
				}
				field(c, "Agent", "", func() {
					choices := []choice{}
					for _, a := range s.Agents {
						choices = append(choices, choice{ID: a.ID, Name: a.Name, Description: a.Command})
					}
					selectChoice(c.Key("agent"), "Configured agent", v.agentID, choices, false, func(id string) { v.agentID = id })
					if command != "" {
						ui.Text(c, command).SingleLine().Font("monospace").FontSize(11.5).TextColor(t.TextMuted).Tooltip(command)
					}
					ui.Row(c).Gap(4).Margin(-2, 0, 0, -7).Children(func() {
						quietButton(c, "Manage agents").OnClick(v.openSettings)
						quietButton(c, "Use custom command").OnClick(func() { v.customCommand = true })
					})
				})
			} else {
				field(c, "Agent command", "Executable and arguments, with quotes for spaces. No shell expansion.", func() {
					ui.TextInput(c.Key("agent-command"), &v.command).Label("Agent command").Placeholder("Executable and arguments").AutoFocus()
					ui.Row(c).Gap(4).Margin(-2, 0, 0, -7).Children(func() {
						quietButton(c, "Save agents in Settings").OnClick(v.openSettings)
						if len(s.Agents) > 0 {
							quietButton(c, "Use saved agent").OnClick(func() { v.customCommand = false })
						}
					})
				})
			}
			field(c, "Project", "Agents run as your user, with your environment and filesystem access. Use commands you trust.", func() {
				folder := "Works in a new, randomly named folder in " + shortPath(s.WorkingDirectory) + "."
				choices := []choice{{ID: "", Name: "No project", Description: "A new folder for this thread"}}
				for _, p := range s.Projects {
					choices = append(choices, choice{ID: p.ID, Name: p.Name, Description: shortPath(p.Dir)})
					if p.ID == v.projectID {
						folder = "Works in " + shortPath(p.Dir) + ", shared by every thread in this project."
					}
				}
				selectChoice(c.Key("project"), "Thread project", v.projectID, choices, false, func(id string) { v.projectID = id })
				ui.Text(c, folder).FontSize(12.5).TextColor(t.TextMuted)
				ui.Row(c).Gap(4).Margin(-2, 0, 0, -7).Children(func() {
					quietButton(c, "Manage projects").OnClick(v.openSettings)
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				custom := v.customCommand || len(s.Agents) == 0
				missing := (custom && strings.TrimSpace(v.command) == "") || (!custom && v.agentID == "")
				ui.PrimaryButton(c, "Create thread").Disabled(missing).OnClick(func() {
					var id string
					var err error
					if custom {
						id, err = v.app.Create(v.command, v.projectID)
					} else {
						id, err = v.app.CreateWithAgent(v.agentID, v.projectID)
					}
					if err != nil {
						v.fail(err)
						return
					}
					v.archived = false
					v.selectThread(id)
				})
			})
		})
	})
}

func (v *View) threadHeader(c *ui.Context, thread app.ThreadView) {
	t := c.Theme()
	busy := thread.Running || thread.Configuring
	ui.Row(c).Height(44).Shrink(0).Padding(0, 10, 0, 20).Gap(2).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).Margin(0, 8, 0, 0).Children(func() {
			// The title keeps priority; agent and path truncate first.
			title := ui.Text(c.Key("thread-title"), thread.Title).FontSize(13).FontWeight(500).SingleLine().Shrink(0).MaxWidthPercent(70).Label("Current thread title").Tooltip(thread.Title + "\nDouble-click to rename")
			if title.DoubleClicked() {
				v.openRename(thread)
			}
			if thread.Archived {
				chip(c, "Archived", t.TextMuted)
			}
			agent := thread.AgentName
			if agent == "" {
				agent = commandName(thread.Command)
			}
			ui.Text(c, joinNonEmpty(" · ", agent, shortPath(thread.Cwd))).FontSize(12.5).TextColor(faint(t)).SingleLine().Shrink(3).MinWidth(0).Tooltip(thread.Command + "\n" + thread.Cwd)
		})
		if v.forkResult != nil {
			ui.Spinner(c).Size(14, 14).Label("Forking session").Margin(0, 6)
		}
		iconAction(c, "Fork", forkIcon, busy || thread.SessionID == "" || v.forkResult != nil).Tooltip("Fork: the agent branches this session into a new thread").OnClick(func() { v.fork(thread.ID) })
		archive, icon := "Archive", archiveIcon
		if thread.Archived {
			archive, icon = "Restore", restoreIcon
		}
		iconAction(c, archive, icon, busy).OnClick(func() { v.archive(thread) })
		iconButton(c, "More thread actions", moreIcon).Menu(func(m *ui.Menu) {
			if m.Item("Rename…").Chosen() {
				v.openRename(thread)
			}
			reload := "Retry connection"
			if thread.Connected {
				reload = "Reload history"
			}
			if m.Item(reload).Disabled(thread.Archived || busy || thread.SessionID == "").Chosen() {
				v.fail(v.app.Reload(thread.ID))
			}
			if m.Item("Session options…").Disabled(thread.Archived).Chosen() {
				v.openSessionOptions(thread)
			}
			m.Separator()
			if m.Item("Remove from Mu…").Disabled(busy).Chosen() {
				v.confirmRemove(thread.ID)
			}
		})
	})
}

func (v *View) thread(c *ui.Context, thread app.ThreadView) {
	t := c.Theme()
	ui.Column(c.Key(thread.ID)).Grow(1).MinHeight(0).Children(func() {
		v.threadHeader(c, thread)
		if thread.TitleError != "" {
			v.errorBox(c, thread.TitleError, func() {
				iconButton(c, "Dismiss title error", closeIcon).OnClick(func() { v.app.DismissTitleError(thread.ID) })
			})
		}
		if thread.SessionError != "" {
			v.errorBox(c, thread.SessionError, func() {
				if !thread.Archived && !thread.Running && !thread.Configuring && thread.SessionID != "" && !thread.Connected {
					ui.Button(c, "Retry connection").OnClick(func() { v.fail(v.app.Reload(thread.ID)) })
				}
			})
		}
		list := v.lists[thread.ID]
		if list == nil {
			list = &ui.ListState{FollowEnd: true}
			v.lists[thread.ID] = list
		}
		list.Key = func(i int) any { return thread.Entries[i].ID }
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			ui.List(c.Key("messages"), list, len(thread.Entries), func(i int) {
				top := float32(18)
				if i == 0 {
					top = 0
				} else if activity(thread.Entries[i-1].Role) && activity(thread.Entries[i].Role) {
					top = 2
				}
				ui.Row(c).Justify(ui.Center).Padding(top, 24, 0, 24).Children(func() {
					ui.Column(c).Grow(1).MaxWidth(readingWidth).MinWidth(0).Children(func() { v.entry(c, thread.ID, thread.Entries[i]) })
				})
			}).Grow(1).MinHeight(0).Padding(20, 0, 20, 0).Children(func() {
				if len(thread.Entries) == 0 {
					text := "Send a message to start the agent."
					if thread.SessionID != "" && !thread.Connected {
						text = "History is stored by the agent. Load the session to view it."
					}
					if thread.Configuring {
						text = "Loading session from agent…"
					}
					if thread.Archived {
						text = "Restore this thread to load its history from the agent."
					}
					ui.Column(c).Padding(48, 24).AlignItems(ui.Center).Gap(8).Children(func() {
						if thread.Configuring {
							ui.Spinner(c).Size(16, 16)
						}
						ui.Text(c, text).FontSize(13).TextColor(t.TextMuted)
					})
				}
			})
			if !list.AtEnd() && len(thread.Entries) > 0 {
				jump := ui.ButtonBase(c).Transition(colorMotion).Label("Jump to latest").Attach(ui.AnchorBottom, ui.AnchorBottom).Bottom(10).Height(26).Padding(0, 10).Radius(6).Border(1, hairline(t)).Gap(6)
				if jump.Hovered() {
					jump.Background(t.SurfaceHover)
				} else {
					jump.Background(t.Surface)
				}
				jump.Children(func() {
					ui.Icon(c, chevronIcon).Size(13, 13).TextColor(t.TextMuted)
					ui.Text(c, "Jump to latest").FontSize(12)
				}).OnClick(list.ScrollToEnd)
			}
		})
		if thread.Notice != "" {
			ui.Text(c, thread.Notice).Padding(0, 24, 8, 24).FontSize(12).TextColor(t.TextMuted).AlignSelf(ui.Center)
		}
		if len(thread.Permissions) > 0 {
			// Several requests, or a large one, scroll instead of pushing the
			// composer and Stop out of the window.
			_, height := c.Size()
			ui.Scroll(c).Shrink(0).MaxHeight(height * 0.5).Children(func() {
				for _, p := range thread.Permissions {
					v.permission(c, thread.ID, p)
				}
			})
		}
		if thread.Error != "" {
			v.errorBox(c, thread.Error)
		}
		v.composer(c, thread)
	})
}

func activity(role string) bool { return role == "tool" || role == "thought" || role == "plan" }

func (v *View) composer(c *ui.Context, thread app.ThreadView) {
	t := c.Theme()
	ui.Row(c.Key("composer")).Shrink(0).Justify(ui.Center).Padding(4, 24, 10, 24).Children(func() {
		ui.Column(c).Grow(1).MaxWidth(readingWidth).MinWidth(0).Gap(6).Children(func() {
			if thread.Archived {
				ui.Row(c).Padding(8, 8, 8, 14).Gap(10).Radius(8).Border(1, hairline(t)).Background(t.Surface).Children(func() {
					ui.Text(c, "Archived. Restore this thread to continue.").FontSize(13).TextColor(t.TextMuted).Grow(1).MinWidth(0)
					ui.Button(c, "Restore thread").OnClick(func() { v.archive(thread) })
				})
				v.usageLine(c, thread, false)
				return
			}
			box := ui.Column(c.Key("composer-box")).Transition(colorMotion).Radius(8).Border(1, hairline(t)).Background(t.Surface)
			box.Children(func() {
				if v.messageInput(c, thread, box).Focused() {
					box.BorderColor(hairline(t).Mix(t.Text, 0.18))
				}
				ui.Row(c).Padding(0, 6, 6, 7).Gap(2).Children(func() {
					ui.Row(c).Grow(1).MinWidth(0).Gap(2).ClipX().Children(func() { v.inlineOptions(c, thread) })
					iconAction(c, "Session options", slidersIcon, false).OnClick(func() { v.openSessionOptions(thread) })
					if thread.Running || thread.Configuring {
						status := "Updating session…"
						if thread.Running {
							status = thread.Status
						}
						ui.Spinner(c).Size(13, 13).Label("Agent working").Margin(0, 4, 0, 8)
						ui.Text(c, status).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0).Margin(0, 6, 0, 0)
					}
					if thread.Running {
						roundButton(c, "Stop", stopIcon, false, false).OnClick(func() { v.app.Stop(thread.ID) })
					} else {
						disabled := thread.Configuring || strings.TrimSpace(*v.draft(thread.ID)) == ""
						roundButton(c, "Send", sendIcon, true, disabled).OnClick(func() { v.send(thread.ID, *v.draft(thread.ID)) })
					}
				})
			})
			// The keyboard hint is the first thing to go in a narrow window.
			v.usageLine(c, thread, box.Bounds().W >= 600)
		})
	})
}

// roundButton is the composer's send/stop action: the accent only while
// there is something to send, otherwise a quiet outlined square.
func roundButton(c *ui.Context, label string, icon *ui.SVG, primary, disabled bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).Tooltip(label).Size(26, 26).Radius(6).Shrink(0).Disabled(disabled)
	bg, fg, border := ui.Transparent, t.Text, hairline(t)
	switch {
	case disabled:
		fg = faint(t)
	case primary && b.Hovered():
		bg, fg, border = t.AccentHover, t.AccentText, ui.Transparent
	case primary:
		bg, fg, border = t.Accent, t.AccentText, ui.Transparent
	case b.Hovered():
		bg = t.Text.Alpha(0.06)
	}
	b.Background(bg).Border(1, border).Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(fg) })
	return b
}

// inlineOptions shows the agent's advertised select options in the composer
// toolbar. Boolean options and descriptions stay in the session dialog.
func (v *View) inlineOptions(c *ui.Context, thread app.ThreadView) {
	disabled := !thread.Connected || thread.Configuring || thread.Archived
	for _, o := range thread.Session.Options {
		if o.Type != acp.SessionConfigOptionTypeSelect {
			continue
		}
		choices := selectChoices(o)
		ui.Box(c.Key("inline-" + string(o.ID))).Shrink(1).MinWidth(0).Children(func() {
			chipSelect(c, o.Name, o.Name, selectValue(o), choices, disabled || len(choices) == 0, func(value string) { v.fail(v.app.SetOption(thread.ID, string(o.ID), value)) })
		})
	}
	if len(thread.Session.Options) == 0 && thread.Session.Modes != nil {
		chipSelect(c.Key("inline-mode"), "Mode", "Mode", string(thread.Session.Modes.CurrentModeID), modeChoices(thread.Session.Modes), disabled, func(value string) { v.fail(v.app.SetMode(thread.ID, value)) })
	}
}

// selectValue is a select option's current value ID.
func selectValue(o acp.SessionConfigOption) string {
	switch v := o.CurrentValue.(type) {
	case acp.SessionConfigValueId:
		return string(v)
	case string:
		return v
	}
	return ""
}

func selectChoices(o acp.SessionConfigOption) []choice {
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
	if o.Options.Groups != nil {
		for _, g := range *o.Options.Groups {
			add(g.Options, g.Name)
		}
	}
	return choices
}

func modeChoices(modes *acp.SessionModeState) []choice {
	choices := []choice{}
	for _, m := range modes.AvailableModes {
		item := choice{ID: string(m.ID), Name: m.Name}
		if m.Description != nil {
			item.Description = *m.Description
		}
		choices = append(choices, item)
	}
	return choices
}

func (v *View) entry(c *ui.Context, threadID string, e state.Entry) {
	t := c.Theme()
	details := func() {
		if e.Details != "" {
			smallDisclosure(c, "Content details", v.disclosure("content"+threadID+e.ID), func() {
				detailText(c, e.Details)
			})
		}
	}
	switch e.Role {
	case "assistant":
		ui.Column(c).Gap(6).Children(func() {
			markdown.View(c, e.Text, &v.md)
			details()
		})
	case "user":
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.Column(c).MaxWidthPercent(85).Shrink(1).MinWidth(0).Padding(8, 12).Radius(8).Border(1, hairline(t)).Background(t.Surface).Gap(6).Children(func() {
				ui.Text(c, e.Text).FontSize(14).LineHeight(1.5).Selectable()
				details()
			})
		})
	case "tool", "thought", "plan":
		v.activity(c, threadID, e)
	default:
		ui.Text(c, e.Text).Selectable().FontSize(12.5).TextColor(t.TextMuted)
	}
}

// activity is a compact, collapsed-by-default row for tool calls, thinking
// and plans, so prose stays the focus of the transcript.
func (v *View) activity(c *ui.Context, threadID string, e state.Entry) {
	t := c.Theme()
	label, icon := e.Title, toolIcon
	switch e.Role {
	case "thought":
		label, icon = "Thinking", thoughtIcon
	case "plan":
		label, icon = "Plan", planIcon
	}
	if label == "" {
		label = "Tool call"
	}
	p := ui.CollapsibleBase(c, v.disclosure(threadID+e.ID))
	trigger := p.Trigger.Transition(colorMotion).Label(label).AlignSelf(ui.Start).MinWidth(0).Height(26).Padding(0, 8, 0, 4).Radius(5).Gap(7)
	hovered := trigger.Hovered()
	if hovered {
		trigger.Background(t.Text.Alpha(0.045))
	}
	trigger.Children(func() {
		ui.Icon(c, disclosure).Size(11, 11).TextColor(faint(t)).Rotate(90 * p.Progress()).Shrink(0)
		ui.Icon(c, icon).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
		text := ui.Text(c, label).FontSize(13).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
		if hovered {
			text.TextColor(t.Text)
		}
		if e.Status != "" {
			status := strings.ReplaceAll(e.Status, "_", " ")
			color := t.TextMuted
			switch e.Status {
			case "completed":
				color = okColor(t)
			case "failed":
				color = t.Danger
			case "in_progress":
				color = t.Accent
			}
			chip(c, status, color)
		}
	})
	p.Panel(func() {
		switch {
		case e.Role == "plan":
			markdown.View(c, e.Text, &v.md)
		case e.Text == "":
		case e.Role == "tool":
			ui.Text(c, e.Text).Selectable().Font("monospace").FontSize(12).Padding(8, 10).Radius(6).Border(1, t.Border).Background(t.Surface)
		default:
			ui.Text(c, e.Text).Selectable().FontSize(13).LineHeight(1.5).TextColor(t.TextMuted)
		}
		if e.Details != "" {
			smallDisclosure(c, "Tool / protocol details", v.disclosure("details"+threadID+e.ID), func() {
				detailText(c, e.Details)
			})
		}
	}).Padding(4, 0, 6, 26).Gap(6)
}

func (v *View) permission(c *ui.Context, id string, p app.Permission) {
	t := c.Theme()
	ui.Row(c.Key(p.ID)).Shrink(0).Justify(ui.Center).Padding(0, 24, 8, 24).Children(func() {
		ui.Column(c).Grow(1).MaxWidth(readingWidth).MinWidth(0).Padding(12, 14).Gap(10).Radius(8).Border(1, hairline(t)).Background(t.Surface).Children(func() {
			title := "Permission needed"
			if p.Tool.Title != "" {
				title += " for " + p.Tool.Title
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Icon(c, warningIcon).Size(15, 15).TextColor(warnColor(t))
				ui.Text(c, title).FontSize(13).FontWeight(500).Grow(1).MinWidth(0)
				if kind := toolKindLabel(p.Tool.Kind); kind != "" {
					ui.Text(c, kind).FontSize(11.5).TextColor(t.TextMuted).Shrink(0)
				}
			})
			if p.Tool.Text != "" {
				ui.Scroll(c).MaxHeight(120).Radius(6).Border(1, t.Border).Background(t.Background).Padding(8, 10).Children(func() {
					ui.Text(c, strings.TrimRight(p.Tool.Text, "\n")).Selectable().Font("monospace").FontSize(11.5).TextColor(t.TextMuted)
				})
			}
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, o := range p.Request.Options {
					var button ui.Element
					if strings.HasPrefix(string(o.Kind), "allow") {
						button = ui.PrimaryButton(c.Key(string(o.OptionID)), o.Name)
					} else {
						button = ui.Button(c.Key(string(o.OptionID)), o.Name)
					}
					button.OnClick(func() { v.app.Answer(id, p.ID, o.OptionID) })
				}
				if len(p.Request.Options) == 0 {
					ui.Text(c, "The agent offered no options. Stop the turn to cancel.").FontSize(12).TextColor(t.TextMuted)
				}
			})
			// Details follow the options, so expanding them never hides the choice.
			smallDisclosure(c, "Tool / protocol details", v.disclosure("permission"+id+p.ID), func() {
				ui.Scroll(c).MaxHeight(160).Children(func() { detailText(c, p.Tool.Details) })
			})
		})
	})
}

// toolKindLabel names an ACP tool kind, such as "Execute" or "Switch mode".
// The catch-all "other" kind adds nothing and is omitted.
func toolKindLabel(kind string) string {
	if kind == "" || kind == string(acp.ToolKindOther) {
		return ""
	}
	label := strings.ReplaceAll(kind, "_", " ")
	return strings.ToUpper(label[:1]) + label[1:]
}

func (v *View) errorBox(c *ui.Context, text string, actions ...func()) {
	t := c.Theme()
	ui.Row(c).Shrink(0).Margin(8, 16, 0, 16).Padding(6, 6, 6, 12).Gap(10).Radius(6).Background(t.Danger.Alpha(0.07)).Border(1, t.Danger.Alpha(0.22)).Children(func() {
		dot(c, t.Danger).Size(6, 6)
		ui.Scroll(c).Grow(1).MinWidth(0).MaxHeight(96).Children(func() {
			ui.Text(c, text).Selectable().FontSize(12.5)
		})
		for _, action := range actions {
			action()
		}
	})
}

func (v *View) dismissibleError(c *ui.Context, text string) {
	v.errorBox(c, text, func() {
		iconButton(c, "Dismiss error", closeIcon).OnClick(func() { v.err = "" })
	})
}
