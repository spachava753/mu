package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/state"
)

func (v *View) openSettings() {
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
}

func (v *View) settings(c *ui.Context, s app.Snapshot) {
	ui.Row(c).Padding(20, 24).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
		ui.Text(c, "Settings").FontSize(20).Bold().Grow(1)
		ui.Button(c, "Done").OnClick(func() { v.settingsOpen = false; v.err = "" })
	})
	ui.Scroll(c).Grow(1).Padding(24).Children(func() {
		ui.Column(c).MaxWidth(640).Gap(14).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, "Agents").FontSize(16).Bold().Grow(1)
				ui.Button(c, "Add agent").OnClick(func() {
					v.editAgent = state.AgentConfig{}
					v.makeDefault = len(s.Agents) == 0
					v.settingsNotice, v.err = "", ""
				})
			})
			ui.Text(c, "Save commands once. The default agent is selected for new threads.").FontSize(12).TextColor(c.Theme().TextMuted)
			for _, agent := range s.Agents {
				row := ui.ButtonBase(c.Key(agent.ID)).Transition(colorMotion).Label("Edit agent: "+agent.Name).FillWidth().Padding(10, 12).Radius(6).Border(1, c.Theme().Border)
				row.Background(rowColor(c.Theme(), agent.ID == v.editAgent.ID, row.Hovered()))
				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Text(c, agent.Name).Bold()
						ui.Text(c, agent.Command).SingleLine().FontSize(12).TextColor(c.Theme().TextMuted)
					})
					if agent.ID == s.DefaultAgentID {
						ui.Text(c, "Default").FontSize(11).TextColor(c.Theme().TextMuted)
					}
				}).OnClick(func() {
					v.editAgent = agent
					v.makeDefault = agent.ID == s.DefaultAgentID
					v.settingsNotice, v.err = "", ""
				})
			}
			ui.Divider(c).Margin(6, 0)
			heading := "Add an agent"
			if v.editAgent.ID != "" {
				heading = "Edit agent"
			}
			ui.Text(c, heading).Bold()
			ui.Text(c, "Name").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.TextInput(c.Key("agent-name"), &v.editAgent.Name).Label("Agent name").Placeholder("A name for this configuration")
			ui.Text(c, "Command").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.TextInput(c.Key("saved-command"), &v.editAgent.Command).Label("Saved agent command").Placeholder("Executable and arguments")
			ui.Text(c, "Commands run exactly as entered, with normal PATH lookup. Quote paths containing spaces. No shell expansion or adapter discovery.").FontSize(12).TextColor(c.Theme().TextMuted)
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
				if v.editAgent.ID != "" {
					ui.Button(c, "Remove agent").TextColor(c.Theme().Danger).OnClick(func() { v.removeAgent = true })
				}
			})
			if v.settingsNotice != "" {
				ui.Text(c, v.settingsNotice).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			ui.Text(c, "Only configure agents you trust. Commands are stored locally in plain text; keep credentials out of them.").Margin(8, 0).FontSize(12).TextColor(c.Theme().TextMuted)
		})
	})
}

func (v *View) removeAgentDialog(c *ui.Context) {
	modal(c, &v.removeAgent, func() {
		ui.Text(c, "Remove this agent configuration?").FontSize(18).Bold()
		ui.Text(c, "Existing threads and their saved commands are not changed.").MaxWidth(400)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
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
	}).Border(1, c.Theme().Border).Shadow(0, 0, 0, 0, ui.Transparent)
}
