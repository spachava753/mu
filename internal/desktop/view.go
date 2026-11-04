// Package desktop is the Go-only MyGo interface. It contains no webview or frontend runtime.
package desktop

import (
	"path/filepath"
	"slices"
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
	settingsOpen, usageOpen              bool
	focused                              string // the thread the app was last told is in view
	drag                                 sidebarDrag
	springing                            bool
	sidebarShown                         float32 // as of the last frame
	picker                               commandPicker
	deleteIDs                            []string
	selection                            threadSelection
	renaming, renameFresh                bool
	renameID, renameTitle, renameError   string
	command, agentID, workDir, projectID string
	projects                             projectEditor
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
	// ChooseFolder shows the native folder picker and returns the chosen path,
	// or "" if cancelled. It blocks, so the view calls it off the UI thread.
	// Without it, a folder can still be typed.
	ChooseFolder func() (string, error)
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
	v.selected, v.creating, v.settingsOpen, v.err = id, false, false, ""
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
	v.creating, v.settingsOpen, v.err = true, false, ""
	s := v.app.Snapshot()
	v.agentID, v.projectID = s.DefaultAgentID, ""
	// Start in the project of the thread being viewed.
	for _, t := range s.Threads {
		if t.ID == v.selected {
			v.projectID = t.ProjectID
		}
	}
}

// openNewIn opens the new-thread form with a project chosen.
func (v *View) openNewIn(projectID string) {
	v.openNew()
	v.projectID = projectID
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
func (v *View) confirmRemove(ids ...string) { v.deleteIDs, v.deleting = ids, true }

// bulkArchive archives or restores the marked threads. Threads it could not
// change stay marked, and say why.
func (v *View) bulkArchive(order []string) {
	result, err := v.app.ArchiveThreads(v.selection.ids(order), !v.archived)
	if err != nil {
		v.fail(err)
		return
	}
	if slices.Contains(result.Changed, v.selected) {
		if v.archived {
			// Restored while open: load it and, as a single Restore does, list
			// it, unless threads left to retry are in this list.
			v.archived = len(result.Skipped) > 0
			v.loadSelected()
		} else {
			v.selected = ""
		}
	}
	v.finishBulk(result)
}

// removeThreads removes the confirmed threads from Mu.
func (v *View) removeThreads() {
	result, err := v.app.DeleteThreads(v.deleteIDs)
	v.deleting = false
	if err != nil {
		v.fail(err)
		return
	}
	for _, id := range result.Changed {
		if v.selected == id {
			v.selected = ""
		}
		delete(v.drafts, id)
		delete(v.lists, id)
	}
	v.finishBulk(result)
}

func (v *View) finishBulk(result app.BulkResult) {
	if len(result.Skipped) > 0 {
		v.selection.keep(result.Skipped)
		v.err = bulkSkippedText(len(result.Skipped))
		return
	}
	v.err = ""
	v.selection.clear()
}

// bulkSkippedText explains threads a bulk action left unchanged.
func bulkSkippedText(n int) string {
	if n == 1 {
		return "1 thread is busy and was not changed. Stop its turn or wait for its session request, then try again."
	}
	return itoa(n) + " threads are busy and were not changed. Stop their turns or wait for their session requests, then try again."
}

// endSelection leaves selection mode, with any message about it.
func (v *View) endSelection() {
	v.selection.clear()
	v.err = ""
}
func (v *View) fork(id string) {
	result := make(chan forkResult, 1)
	v.forkResult = result
	go func() { id, err := v.app.Fork(id); result <- forkResult{id, err} }()
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
	if v.focused != v.selected {
		v.focused = v.selected
		v.app.FocusThread(v.selected)
	}
	matte(c)
	if v.selection.active() && !v.deleting && !v.renaming && !v.projects.open && !v.projects.removing {
		c.OnShortcut(0, ui.KeyEscape, v.endSelection)
	}
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
		title, body, keep, remove := "Remove this thread from Mu?", "This removes only Mu’s session reference. The agent’s history and workspace files are unchanged. You can import the session again.", "Keep thread", "Remove reference"
		if n := len(v.deleteIDs); n > 1 {
			title, body, keep, remove = "Remove "+itoa(n)+" threads from Mu?", "This removes only Mu’s session references. The agents’ histories and workspace files are unchanged. You can import the sessions again.", "Keep threads", "Remove references"
		}
		ui.Text(c, title).FontSize(14).FontWeight(500)
		ui.Text(c, body).FontSize(13).TextColor(c.Theme().TextMuted).MaxWidth(420)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			ui.Button(c, keep).OnClick(func() { v.deleting = false })
			ui.Button(c, remove).TextColor(c.Theme().Danger).OnClick(v.removeThreads)
		})
	})
	v.renameDialog(c)
	v.projectDialog(c)
	v.removeProjectDialog(c, s)
	v.removeAgentDialog(c)
}

func (v *View) sidebar(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	sidebar := ui.Column(c.Key("sidebar")).Label("Thread sidebar").Shrink(0).ClipX()
	sidebar.Children(func() {
		width, collapsed := v.sidebarHandle(c, sidebar, s)
		toggle := func() {
			v.selection.clear() // The rail has no way to show or act on it.
			v.fail(v.app.SetSidebarCollapsed(!s.SidebarCollapsed))
		}
		if collapsed {
			ui.Column(c.Key("rail")).Width(railWidth-1).Grow(1).AlignItems(ui.Center).Padding(10, 0).Gap(4).Transition(fadeMotion).Children(func() {
				iconButton(c, "Expand sidebar", panelIcon).OnClick(toggle)
				ui.Box(c).Height(8)
				iconButton(c, "New thread", plusIcon).OnClick(v.openNew)
				iconButton(c, "New project", folderPlusIcon).OnClick(v.openNewProject)
				iconButton(c, "Import sessions", importIcon).OnClick(v.openImports)
				ui.Spacer(c)
				iconButton(c, "Settings", gearIcon).OnClick(v.openSettings)
			})
			return
		}
		ui.Column(c.Key("thread-list")).Width(width - 1).Grow(1).MinHeight(0).Transition(fadeMotion).Children(func() {
			ui.Row(c).Height(52).Shrink(0).Padding(0, 10, 0, 18).Gap(2).Children(func() {
				ui.Text(c, "mu").FontSize(14).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
				iconButton(c, "New thread", plusIcon).OnClick(v.openNew)
				iconButton(c, "Collapse sidebar", panelIcon).OnClick(toggle)
			})
			groups := groupThreads(s, v.archived)
			order := threadOrder(groups)
			v.selection.retain(order)
			count := len(order)
			ui.Row(c).Shrink(0).Padding(6, 10, 4, 18).Gap(4).Children(func() {
				label := "Threads"
				if v.archived {
					label = "Archived"
				}
				ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(faint(t)).SingleLine().Shrink(0)
				ui.Text(c, itoa(count)).FontSize(12).TextColor(faint(t)).SingleLine().Grow(1)
				if v.selection.active() {
					quietButton(c.Key("selection-done"), "Done").OnClick(v.endSelection)
					return
				}
				if count > 0 {
					quietButton(c.Key("selection-start"), "Select").OnClick(func() { v.selection.mode = true })
				}
				text, name := "Archived", "Show archived"
				if v.archived {
					text, name = "Active", "Show active"
				}
				quietAction(c.Key("archive-toggle"), text, name, false).Tooltip(name).OnClick(func() { v.archived = !v.archived })
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(1).Padding(2, 10, 8, 10).Children(func() {
				v.threadGroups(c, s, groups, order)
				if len(groups) == 0 {
					text := "No threads yet."
					if v.archived {
						text = "No archived threads."
					}
					ui.Text(c, text).Padding(8).FontSize(12).TextColor(t.TextMuted)
				}
			})
			if v.selection.active() {
				v.bulkBar(c, order)
			}
			ui.Column(c).Shrink(0).Padding(6, 10, 10, 10).Gap(1).Children(func() {
				navRow(c, "New project", folderPlusIcon, false).OnClick(v.openNewProject)
				navRow(c, "Import sessions", importIcon, v.importOpen).OnClick(v.openImports)
				navRow(c, "Settings", gearIcon, v.settingsOpen).OnClick(v.openSettings)
			})
		})
	})
}

// threadGroups lists the visible threads under their sections.
func (v *View) threadGroups(c *ui.Context, s app.Snapshot, groups []threadGroup, order []string) {
	for _, group := range groups {
		switch {
		case group.Project != nil:
			v.projectHeading(c, *group.Project)
			if len(group.Threads) == 0 {
				ui.Text(c, "No threads yet").Padding(2, 8, 4, 8).FontSize(11.5).TextColor(faint(c.Theme()))
			}
		case group.Name != "":
			ui.Text(c.Key("group-"+group.Key), group.Name).Padding(12, 8, 4, 8).FontSize(11.5).FontWeight(500).TextColor(faint(c.Theme())).SingleLine()
		}
		for _, thread := range group.Threads {
			v.threadRow(c, s, thread, order)
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

// bulkBar acts on the marked threads, above the sidebar's destinations.
func (v *View) bulkBar(c *ui.Context, order []string) {
	t := c.Theme()
	n := v.selection.count()
	ui.Row(c.Key("bulk-bar")).Shrink(0).Margin(0, 10).Padding(4, 2, 4, 8).Radius(6).Border(1, hairline(t)).Background(t.Surface).Transition(fadeMotion).Children(func() {
		ui.Text(c, itoa(n)+" selected").FontSize(12).FontWeight(500).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
		archive := "Archive"
		if v.archived {
			archive = "Restore"
		}
		quietAction(c, archive, archive, n == 0).OnClick(func() { v.bulkArchive(order) })
		quietAction(c, "Remove", "Remove", n == 0).OnClick(func() { v.confirmRemove(v.selection.ids(order)...) })
	})
}

func (v *View) threadRow(c *ui.Context, s app.Snapshot, thread app.ThreadView, order []string) {
	t := c.Theme()
	selected := thread.ID == v.selected && !v.creating && !v.settingsOpen && !v.importOpen
	marked := v.selection.has(thread.ID)
	button := ui.ButtonBase(c.Key(thread.ID)).Transition(colorMotion).Label("Thread: "+thread.Title).Padding(6, 8).Radius(5).FillWidth()
	if v.selection.active() {
		button.Checked(marked)
	}
	if marked {
		button.Background(markedColor(t, button.Hovered()))
	} else {
		button.Background(sideColor(t, selected, button.Hovered()))
	}
	if v.selection.reveal == thread.ID {
		button.ScrollIntoView() // The bar may have just taken room from the list.
		v.selection.reveal = ""
	}
	busy := thread.Running || thread.Configuring
	button.Children(func() {
		if v.selection.active() {
			box := ui.Box(c).Size(14, 14).Radius(4).Shrink(0).Center().Margin(0, 8, 0, 0)
			if marked {
				box.Background(t.Accent).Children(func() { ui.Icon(c, checkIcon).Size(11, 11).TextColor(t.AccentText) })
			} else {
				box.Border(1, t.TextMuted.Alpha(0.55))
			}
		}
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
			if agentWorking(thread) {
				shimmerText(c, meta, 11.5, color, color.Mix(t.Text, 0.6)).SingleLine()
			} else {
				text := ui.Text(c, meta).FontSize(11.5).TextColor(faint(t)).SingleLine()
				if status != "" && color != t.TextMuted {
					text.TextColor(color)
				}
			}
		})
	}).OnClick(func() {
		if v.selection.click(order, thread.ID, v.selected, button.ClickModifiers()) {
			v.selectThread(thread.ID)
		}
	}).ContextMenu(func(m *ui.Menu) {
		// A row in a selection of several acts on all of them.
		if marked && v.selection.count() > 1 {
			n := itoa(v.selection.count())
			archive := "Archive "
			if v.archived {
				archive = "Restore "
			}
			if m.Item(archive + n + " threads").Chosen() {
				v.bulkArchive(order)
			}
			m.Separator()
			if m.Item("Remove " + n + " threads from Mu…").Chosen() {
				v.confirmRemove(v.selection.ids(order)...)
			}
			return
		}
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
					quietAction(c, "New project", "New project for this thread", false).OnClick(v.openNewProject)
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
			ui.Scroll(c).Label("Permission requests").Shrink(1).MinHeight(0).MaxHeight(height * 0.5).Children(func() {
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
				v.gitLine(c, thread.Git, thread.PullRequest)
				return
			}
			box := ui.Column(c.Key("composer-box")).Transition(colorMotion).Radius(8).Border(1, hairline(t)).Background(t.Surface)
			box.Children(func() {
				if v.messageInput(c, thread, box).Focused() {
					box.BorderColor(hairline(t).Mix(t.Text, 0.18))
				}
				ui.Row(c).Padding(0, 6, 6, 7).Gap(2).Children(func() {
					// An agent may advertise more options than fit: they scroll, so the
					// draft and its actions stay in the window. The padding keeps focus
					// rings clear of the scroll area's clip without moving the chips.
					ui.Scroll(c.Key("composer-options")).Label("Session options").Grow(1).MinWidth(0).MaxHeight(optionsHeight + 6).Padding(3).Margin(-3).Children(func() {
						ui.Row(c).Gap(2).Wrap().Children(func() { v.inlineOptions(c, thread) })
					})
					// With several rows of options, the actions stay on the last.
					ui.Row(c).AlignSelf(ui.End).Shrink(0).Gap(2).Children(func() {
						if thread.Running || thread.Configuring {
							status := "Updating session…"
							if thread.Running {
								status = thread.Status
							}
							ui.Spinner(c).Size(13, 13).Label("Agent working").Margin(0, 4, 0, 8)
							var label ui.Element
							if agentWorking(thread) {
								label = shimmerText(c, status, 12, t.TextMuted, t.Text)
							} else {
								label = ui.Text(c, status).FontSize(12).TextColor(t.TextMuted)
							}
							label.SingleLine().MaxWidth(statusWidth).Margin(0, 6, 0, 0)
						}
						if thread.Running {
							roundButton(c, "Stop", stopIcon, false, false).OnClick(func() { v.app.Stop(thread.ID) })
						} else {
							disabled := thread.Configuring || strings.TrimSpace(*v.draft(thread.ID)) == ""
							roundButton(c, "Send", sendIcon, true, disabled).OnClick(func() { v.send(thread.ID, *v.draft(thread.ID)) })
						}
					})
				})
			})
			// The keyboard hint is the first thing to go in a narrow window.
			v.usageLine(c, thread, box.Bounds().W >= 600)
			v.gitLine(c, thread.Git, thread.PullRequest)
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

// inlineOptions shows every option the agent advertises as a menu in the
// composer toolbar, or legacy modes when it advertises none. A new thread has
// no options until its session exists, so it offers to connect.
func (v *View) inlineOptions(c *ui.Context, thread app.ThreadView) {
	if thread.SessionID == "" && !thread.Connected && !thread.Configuring && !thread.Running {
		quietAction(c, "Connect session", "Connect session", false).Tooltip("Load the options this agent offers. No prompt is sent.").OnClick(func() { v.fail(v.app.Connect(thread.ID)) })
	}
	disabled := !thread.Connected || thread.Configuring || thread.Archived
	for _, o := range thread.Session.Options {
		about := ""
		if o.Description != nil {
			about = *o.Description
		}
		ui.Box(c.Key("inline-" + string(o.ID))).Shrink(1).MinWidth(0).Children(func() {
			switch o.Type {
			case acp.SessionConfigOptionTypeSelect:
				choices := selectChoices(o)
				chipSelect(c, o.Name, o.Name, about, selectValue(o), choices, disabled || len(choices) == 0, func(value string) { v.fail(v.app.SetOption(thread.ID, string(o.ID), value)) })
			case acp.SessionConfigOptionTypeBoolean:
				on, _ := o.CurrentValue.(bool)
				chipSelect(c, o.Name, o.Name, about, booleanValue(on), booleanChoices, disabled, func(value string) { v.fail(v.app.SetOption(thread.ID, string(o.ID), value == booleanValue(true))) })
			}
		})
	}
	if len(thread.Session.Options) == 0 && thread.Session.Modes != nil {
		chipSelect(c.Key("inline-mode"), "Mode", "Mode", "", string(thread.Session.Modes.CurrentModeID), modeChoices(thread.Session.Modes), disabled, func(value string) { v.fail(v.app.SetMode(thread.ID, value)) })
	}
	if thread.Connected && !thread.Configuring && len(thread.Session.Options) == 0 && thread.Session.Modes == nil {
		ui.Text(c, "No session options").FontSize(12).TextColor(faint(c.Theme())).SingleLine().Padding(3, 7)
	}
}

// booleanChoices are the menu of a boolean option.
var booleanChoices = []choice{{ID: booleanValue(true), Name: "On"}, {ID: booleanValue(false), Name: "Off"}}

func booleanValue(on bool) string {
	if on {
		return "on"
	}
	return "off"
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
