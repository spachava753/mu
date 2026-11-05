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
	v.settingsOpen, v.err, v.settingsNotice = true, "", ""
	v.app.RefreshBuiltins()
	s := v.app.Snapshot()
	v.builtins.load(s.Builtins)
	v.editAgent = state.AgentConfig{}
	if _, ok := builtinByID(s, s.DefaultAgentID); ok {
		v.editAgent.ID = s.DefaultAgentID // A built-in opens under its own row.
	}
	for _, a := range s.Agents {
		if a.ID == s.DefaultAgentID {
			v.editAgent = a
			break
		}
	}
	v.makeDefault = v.editAgent.ID != "" && v.editAgent.ID == s.DefaultAgentID
	v.workDir = s.WorkingDirectory
	v.worktreeTemplate = s.WorktreeTemplate
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
			v.worktreeSettings(c, s)
			v.projectSettings(c, s)
			ui.Row(c).Gap(8).Margin(12, 0, 0, 0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, "Agents").FontSize(13).FontWeight(500)
					ui.Text(c, "The default agent is selected for new threads.").FontSize(11.5).TextColor(t.TextMuted)
				})
				ui.Button(c, "Add custom agent").OnClick(func() {
					v.editAgent = state.AgentConfig{}
					v.makeDefault = len(s.Agents) == 0
					v.settingsNotice, v.err = "", ""
				})
			})
			if len(s.Builtins) > 0 {
				v.builtinAgents(c, s)
				agentsCaption(c, "Custom", "Any ACP agent, started with a command you enter.")
			}
			ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
				if len(s.Agents) == 0 {
					ui.Text(c, "No custom agents.").Padding(10, 14).FontSize(12.5).TextColor(t.TextMuted)
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
			if !v.builtinOpen(s) {
				v.agentForm(c, s)
			}
			ui.Text(c, "Only configure agents you trust. Commands are stored locally in plain text; keep credentials out of them.").FontSize(11.5).TextColor(t.TextMuted)
		})
	})
}

// agentForm adds or edits a custom agent.
func (v *View) agentForm(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	ui.Column(c).Margin(6, 0, 0, 0).Padding(16).Gap(16).Radius(8).Border(1, t.Border).Background(t.Surface).Children(func() {
		heading := "Add a custom agent"
		if v.editAgent.ID != "" {
			heading = "Edit custom agent"
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
