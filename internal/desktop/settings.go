package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/state"
)

func (v *View) openSettings() {
	v.importOpen = false
	v.picker, v.usageOpen = commandPicker{}, false
	v.settingsOpen, v.sessionOpen, v.err, v.settingsNotice = true, false, "", ""
	s := v.app.Snapshot()
	v.editAgent = state.AgentConfig{}
	for _, a := range s.Agents {
		if a.ID == s.DefaultAgentID {
			v.editAgent = a
			break
		}
	}
	v.makeDefault = v.editAgent.ID != "" && v.editAgent.ID == s.DefaultAgentID
	v.workDir = s.WorkingDirectory
	v.editProject, v.projectNotice, v.projectForm = state.Project{}, "", false
}

func (v *View) settings(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	screenHeader(c, "Settings", "Where threads work, and the agents that run them.", func() {
		ui.Button(c, "Done").OnClick(func() { v.settingsOpen = false; v.err = "" })
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(20, 24).Children(func() {
		ui.Column(c).MaxWidth(640).Gap(14).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "Working directory").FontSize(13).FontWeight(500)
				ui.Text(c, "Each new thread gets its own randomly named folder here. Agent processes also start here.").FontSize(11.5).TextColor(t.TextMuted)
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c.Key("working-directory"), &v.workDir).Label("Working directory").Placeholder("/absolute/path").Grow(1).MinWidth(120)
				ui.Button(c, "Save directory").Shrink(0).Disabled(strings.TrimSpace(v.workDir) == "" || strings.TrimSpace(v.workDir) == s.WorkingDirectory).OnClick(func() {
					if err := v.app.SetWorkingDirectory(v.workDir); err != nil {
						v.fail(err)
						return
					}
					v.workDir, v.err = v.app.Snapshot().WorkingDirectory, ""
				})
			})
			v.projectSettings(c, s)
			ui.Row(c).Gap(8).Margin(12, 0, 0, 0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, "Agents").FontSize(13).FontWeight(500)
					ui.Text(c, "The default agent is selected for new threads.").FontSize(11.5).TextColor(t.TextMuted)
				})
				ui.Button(c, "Add agent").OnClick(func() {
					v.editAgent = state.AgentConfig{}
					v.makeDefault = len(s.Agents) == 0
					v.settingsNotice, v.err = "", ""
				})
			})
			ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
				if len(s.Agents) == 0 {
					ui.Text(c, "No saved agents yet. Add one below.").Padding(12, 14).FontSize(12.5).TextColor(t.TextMuted)
				}
				for i, agent := range s.Agents {
					row := ui.ButtonBase(c.Key(agent.ID)).Transition(colorMotion).Label("Edit agent: "+agent.Name).FillWidth().Padding(9, 14).Gap(10)
					if i > 0 {
						row.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
					}
					selected := agent.ID == v.editAgent.ID
					row.Background(rowColor(t, selected, row.Hovered()))
					row.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							ui.Text(c, agent.Name).FontSize(13).FontWeight(500).SingleLine()
							ui.Text(c, agent.Command).SingleLine().Font("monospace").FontSize(11.5).TextColor(t.TextMuted).Tooltip(agent.Command)
						})
						if agent.ID == s.DefaultAgentID {
							chip(c, "Default", t.TextMuted)
						}
					}).OnClick(func() {
						v.editAgent = agent
						v.makeDefault = agent.ID == s.DefaultAgentID
						v.settingsNotice, v.err = "", ""
					})
				}
			})
			ui.Column(c).Margin(6, 0, 0, 0).Padding(16).Gap(16).Radius(8).Border(1, t.Border).Background(t.Surface).Children(func() {
				heading := "Add an agent"
				if v.editAgent.ID != "" {
					heading = "Edit agent"
				}
				ui.Text(c, heading).FontSize(13).FontWeight(500)
				field(c, "Name", "", func() {
					ui.TextInput(c.Key("agent-name"), &v.editAgent.Name).Label("Agent name").Placeholder("A name for this configuration")
				})
				field(c, "Command", "Runs exactly as entered, with normal PATH lookup. Quote paths containing spaces. No shell expansion or adapter discovery.", func() {
					ui.TextInput(c.Key("saved-command"), &v.editAgent.Command).Label("Saved agent command").Placeholder("Executable and arguments")
				})
				ui.Checkbox(c, &v.makeDefault, "Default for new threads").Disabled(v.editAgent.ID != "" && v.editAgent.ID == s.DefaultAgentID)
				ui.Row(c).Gap(8).Wrap().Children(func() {
					ui.PrimaryButton(c, "Save agent").Disabled(strings.TrimSpace(v.editAgent.Name) == "" || strings.TrimSpace(v.editAgent.Command) == "").OnClick(func() {
						id, err := v.app.SaveAgent(v.editAgent, v.makeDefault)
						if err != nil {
							v.fail(err)
							return
						}
						v.editAgent.ID, v.settingsNotice, v.err = id, "Agent saved. Existing threads keep their original command.", ""
						v.agentID, v.customCommand = v.app.Snapshot().DefaultAgentID, false
						v.makeDefault = id == v.agentID
					})
					if v.editAgent.ID != "" && v.editAgent.ID != s.DefaultAgentID {
						ui.Button(c, "Make default").OnClick(func() {
							v.fail(v.app.SetDefaultAgent(v.editAgent.ID))
							v.agentID = v.app.Snapshot().DefaultAgentID
							v.makeDefault = true
						})
					}
					ui.Spacer(c)
					if v.editAgent.ID != "" {
						ui.Button(c, "Remove agent").TextColor(t.Danger).OnClick(func() { v.removeAgent = true })
					}
				})
				if v.settingsNotice != "" {
					ui.Row(c).Gap(6).Children(func() {
						ui.Icon(c, checkIcon).Size(14, 14).TextColor(okColor(t))
						ui.Text(c, v.settingsNotice).FontSize(12).TextColor(t.TextMuted)
					})
				}
			})
			ui.Text(c, "Only configure agents you trust. Commands are stored locally in plain text; keep credentials out of them.").FontSize(11.5).TextColor(t.TextMuted)
		})
	})
}

func (v *View) removeAgentDialog(c *ui.Context) {
	modal(c, &v.removeAgent, func() {
		ui.Text(c, "Remove this agent configuration?").FontSize(14).FontWeight(500)
		ui.Text(c, "Existing threads and their saved commands are not changed.").FontSize(13).TextColor(c.Theme().TextMuted).MaxWidth(400)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			ui.Button(c, "Keep agent").OnClick(func() { v.removeAgent = false })
			ui.Button(c, "Remove configuration").TextColor(c.Theme().Danger).OnClick(func() {
				if err := v.app.RemoveAgent(v.editAgent.ID); err != nil {
					v.fail(err)
				} else {
					v.editAgent, v.settingsNotice = state.AgentConfig{}, "Agent removed."
					v.agentID = v.app.Snapshot().DefaultAgentID
					v.makeDefault = v.agentID == ""
				}
				v.removeAgent = false
			})
		})
	})
}

// projectSettings lists projects and edits one. A project is a named,
// existing folder that all of its threads work in.
func (v *View) projectSettings(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	ui.Row(c).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, "Projects").FontSize(13).FontWeight(500)
			ui.Text(c, "Every thread in a project works in the project's folder.").FontSize(11.5).TextColor(t.TextMuted)
		})
		ui.Button(c, "Add project").OnClick(func() { v.editProject, v.projectNotice, v.err, v.projectForm = state.Project{}, "", "", true })
	})
	if len(s.Projects) > 0 {
		ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
			for i, project := range s.Projects {
				row := ui.ButtonBase(c.Key(project.ID)).Transition(colorMotion).Label("Edit project: "+project.Name).FillWidth().Padding(9, 14).Gap(10)
				if i > 0 {
					row.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
				}
				row.Background(rowColor(t, project.ID == v.editProject.ID, row.Hovered()))
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, project.Name).FontSize(13).FontWeight(500).SingleLine()
						ui.Text(c, shortPath(project.Dir)).SingleLine().Font("monospace").FontSize(11.5).TextColor(t.TextMuted).Tooltip(project.Dir)
					})
				}).OnClick(func() { v.editProject, v.projectNotice, v.err, v.projectForm = project, "", "", true })
			}
		})
	}
	if v.projectNotice != "" {
		ui.Row(c).Gap(6).Children(func() {
			ui.Icon(c, checkIcon).Size(14, 14).TextColor(okColor(t))
			ui.Text(c, v.projectNotice).FontSize(12).TextColor(t.TextMuted)
		})
	}
	if !v.projectForm {
		return
	}
	ui.Column(c).Padding(16).Gap(16).Radius(8).Border(1, t.Border).Background(t.Surface).Children(func() {
		heading := "Add a project"
		if v.editProject.ID != "" {
			heading = "Edit project"
		}
		ui.Text(c, heading).FontSize(13).FontWeight(500)
		field(c, "Name", "", func() {
			ui.TextInput(c.Key("project-name"), &v.editProject.Name).Label("Project name").Placeholder("A name for this project")
		})
		field(c, "Folder", "An existing folder. Existing threads keep the folder they started in.", func() {
			ui.TextInput(c.Key("project-folder"), &v.editProject.Dir).Label("Project folder").Placeholder("/absolute/path/to/project")
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.PrimaryButton(c, "Save project").Disabled(strings.TrimSpace(v.editProject.Name) == "" || strings.TrimSpace(v.editProject.Dir) == "").OnClick(func() {
				id, err := v.app.SaveProject(v.editProject)
				if err != nil {
					v.fail(err)
					return
				}
				for _, p := range v.app.Snapshot().Projects {
					if p.ID == id {
						v.editProject = p
					}
				}
				v.projectNotice, v.err, v.projectForm = "Project saved.", "", false
			})
			ui.Button(c, "Cancel").OnClick(func() { v.editProject, v.projectForm = state.Project{}, false })
			ui.Spacer(c)
			if v.editProject.ID != "" {
				ui.Button(c, "Remove project").TextColor(t.Danger).OnClick(func() {
					if err := v.app.RemoveProject(v.editProject.ID); err != nil {
						v.fail(err)
						return
					}
					if v.projectID == v.editProject.ID {
						v.projectID = ""
					}
					v.editProject, v.projectForm = state.Project{}, false
					v.projectNotice = "Project removed. Its threads and folder are unchanged."
				})
			}
		})
	})
}
