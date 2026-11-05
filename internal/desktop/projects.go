package desktop

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/state"
)

// folderChoice is the outcome of the native folder picker. An empty path means
// the user cancelled.
type folderChoice struct {
	path string
	err  error
}

// projectEditor is the state of the project dialogs. There is one project
// journey: the sidebar, the new-thread form and Settings all open these.
type projectEditor struct {
	open      bool
	project   state.Project // ID is empty for a new project
	err       string
	forThread bool // the new-thread form opened it, and takes the new project
	choosing  chan folderChoice

	removing bool
	remove   state.Project

	reveal string // a project just created, to bring into view in the sidebar
}

// openProject opens the dialog. A picker still open for an earlier dialog can
// no longer change this one.
func (v *View) openProject(project state.Project, forThread bool) {
	v.projects.open, v.projects.project, v.projects.err, v.projects.forThread, v.projects.choosing = true, project, "", forThread, nil
}

// openNewProject opens the dialog for a new project. If the new-thread form is
// showing, the project becomes that thread's.
func (v *View) openNewProject() { v.openProject(state.Project{}, v.creating) }

func (v *View) confirmRemoveProject(project state.Project) {
	v.projects.open, v.projects.removing, v.projects.remove = false, true, project
}

// chooseFolder runs the native picker off the UI thread. The dialog collects
// the result while it draws.
func (v *View) chooseFolder() {
	result, pick := make(chan folderChoice, 1), v.ChooseFolder
	v.projects.choosing = result
	go func() {
		path, err := pick()
		result <- folderChoice{path, err}
	}()
}

func (v *View) collectFolder(c *ui.Context) {
	p := &v.projects
	if p.choosing == nil {
		return
	}
	select {
	case chosen := <-p.choosing:
		p.choosing = nil
		switch {
		case chosen.err != nil:
			p.err = "Could not open the folder picker: " + chosen.err.Error() + ". Enter the path instead."
		case chosen.path != "":
			p.err, p.project.Dir = "", chosen.path
			if strings.TrimSpace(p.project.Name) == "" {
				p.project.Name = app.SuggestProjectName(chosen.path)
			}
		}
	default:
		c.After(100 * time.Millisecond) // Nothing else wakes the view for it.
	}
}

func (v *View) saveProject() {
	p := &v.projects
	if strings.TrimSpace(p.project.Name) == "" || strings.TrimSpace(p.project.Dir) == "" {
		return
	}
	created := p.project.ID == ""
	id, err := v.app.SaveProject(p.project)
	if err != nil {
		p.err = sentence(err.Error())
		return
	}
	if p.forThread {
		v.projectID = id
	}
	if created {
		// Show it: new projects are listed only among active threads, and
		// can land below the fold.
		v.archived, p.reveal = false, id
		v.selection.clear()
	}
	p.open = false
}

func (v *View) projectDialog(c *ui.Context) {
	p := &v.projects
	t := c.Theme()
	modal(c, &p.open, func() {
		v.collectFolder(c)
		heading, action := "New project", "Create project"
		if p.project.ID != "" {
			heading, action = "Edit project", "Save project"
		}
		ui.Column(c).Width(420).MaxWidthPercent(100).Gap(14).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, heading).FontSize(14).FontWeight(500)
				ui.Text(c, "A project is a name for an existing folder. Its threads work in that folder or, in a Git repository, in worktrees of it.").FontSize(12).TextColor(t.TextMuted)
			})
			field(c, "Folder", "", func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.TextInput(c.Key("project-folder"), &p.project.Dir).Label("Project folder").Placeholder("/absolute/path/to/project").Grow(1).MinWidth(0).OnSubmit(v.saveProject)
					if v.ChooseFolder != nil {
						ui.Button(c, "Choose…").Shrink(0).Disabled(p.choosing != nil).OnClick(v.chooseFolder)
					}
				})
			})
			field(c, "Name", "", func() {
				ui.TextInput(c.Key("project-name"), &p.project.Name).Label("Project name").Placeholder("A name for this project").OnSubmit(v.saveProject)
			})
			if p.project.ID != "" {
				ui.Text(c, "Existing threads keep the folder they started in.").FontSize(12).TextColor(t.TextMuted)
			}
			if p.err != "" {
				ui.Text(c, p.err).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				if p.project.ID != "" {
					ui.Button(c, "Remove project…").TextColor(t.Danger).OnClick(func() { v.confirmRemoveProject(p.project) })
				}
				ui.Spacer(c)
				ui.Button(c, "Cancel").OnClick(func() { p.open = false })
				ui.PrimaryButton(c, action).Disabled(strings.TrimSpace(p.project.Name) == "" || strings.TrimSpace(p.project.Dir) == "").OnClick(v.saveProject)
			})
		})
	})
}

// removeProjectDialog says plainly what removing a project does not touch.
func (v *View) removeProjectDialog(c *ui.Context, s app.Snapshot) {
	p := &v.projects
	modal(c, &p.removing, func() {
		threads := 0
		for _, t := range s.Threads {
			if t.ProjectID == p.remove.ID {
				threads++
			}
		}
		body := "Mu forgets this project. Its folder and files are not deleted."
		switch {
		case threads == 1:
			body = "Mu forgets this project. Its thread stays and keeps working in the same folder, and the folder and its files are not deleted."
		case threads > 1:
			body = "Mu forgets this project. Its " + itoa(threads) + " threads stay and keep working in the same folder, and the folder and its files are not deleted."
		}
		ui.Text(c, "Remove project “"+p.remove.Name+"”?").FontSize(14).FontWeight(500).MaxWidth(420)
		ui.Text(c, body).FontSize(13).TextColor(c.Theme().TextMuted).MaxWidth(420)
		ui.Text(c, shortPath(p.remove.Dir)).Font("monospace").FontSize(11.5).TextColor(c.Theme().TextMuted).MaxWidth(420).SingleLine().Tooltip(p.remove.Dir)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			ui.Button(c, "Keep project").OnClick(func() { p.removing = false })
			ui.Button(c, "Remove project").TextColor(c.Theme().Danger).OnClick(func() {
				p.removing = false
				if err := v.app.RemoveProject(p.remove.ID); err != nil {
					v.fail(err)
					return
				}
				if v.projectID == p.remove.ID {
					v.projectID = ""
				}
			})
		})
	})
}

// sentence capitalizes an error for display on its own.
func sentence(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// projectHeading is a project's section in the sidebar, with what can be done
// with it next to its name.
func (v *View) projectHeading(c *ui.Context, s app.Snapshot, project state.Project) {
	t := c.Theme()
	actions := func(m *ui.Menu) {
		if m.Item("New thread").Chosen() {
			v.openNewIn(project.ID)
		}
		if listing := s.Worktrees[project.ID]; listing != nil && listing.Repo() && m.Item("Worktrees…").Chosen() {
			v.openWorktrees(project.ID)
		}
		if m.Item("Edit project…").Chosen() {
			v.openProject(project, false)
		}
		m.Separator()
		if m.Item("Remove project…").Chosen() {
			v.confirmRemoveProject(project)
		}
	}
	heading := ui.Row(c.Key("group-"+project.ID)).Padding(8, 0, 2, 8).Gap(2).ContextMenu(actions)
	if v.projects.reveal == project.ID {
		heading.ScrollIntoView()
		v.projects.reveal = ""
	}
	heading.Children(func() {
		ui.Text(c, project.Name).FontSize(11.5).FontWeight(500).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0).Tooltip(project.Name + "\n" + project.Dir)
		smallIconButton(c, "New thread in "+project.Name, plusIcon).OnClick(func() { v.openNewIn(project.ID) })
		smallIconButton(c, "Project actions: "+project.Name, moreIcon).Menu(actions)
	})
}

// projectSettings lists the projects in Settings; the dialog edits them.
func (v *View) projectSettings(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	ui.Row(c).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, "Projects").FontSize(13).FontWeight(500)
			ui.Text(c, "A project's threads work in its folder or, in a Git repository, in worktrees of it.").FontSize(11.5).TextColor(t.TextMuted)
		})
		ui.Button(c, "New project").OnClick(func() { v.openProject(state.Project{}, false) })
	})
	if len(s.Projects) == 0 {
		return
	}
	ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
		for i, project := range s.Projects {
			row := ui.ButtonBase(c.Key(project.ID)).Transition(colorMotion).Label("Edit project: "+project.Name).FillWidth().Padding(9, 14).Gap(10)
			if i > 0 {
				row.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			}
			row.Background(rowColor(t, false, row.Hovered()))
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, project.Name).FontSize(13).FontWeight(500).SingleLine()
					ui.Text(c, shortPath(project.Dir)).SingleLine().Font("monospace").FontSize(11.5).TextColor(t.TextMuted).Tooltip(project.Dir)
				})
				ui.Text(c, "Edit").FontSize(12).TextColor(t.TextMuted).Shrink(0)
			}).OnClick(func() { v.openProject(project, false) })
		}
	})
}
