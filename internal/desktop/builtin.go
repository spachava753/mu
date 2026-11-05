package desktop

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/state"
)

// comingSoon are agents with adapters still to come, shown dimmed in Settings.
var comingSoon = []struct{ mark, name string }{{"C", "Claude Code"}, {"O", "OpenCode"}, {"X", "Codex"}}

// builtinEditor holds what's being typed for each built-in, and which command was
// just copied, between frames.
type builtinEditor struct {
	executable map[string]*string
	copied     string
}

func newBuiltinEditor() builtinEditor {
	return builtinEditor{executable: map[string]*string{}}
}

// load starts each field from what's saved.
func (e *builtinEditor) load(builtins []app.BuiltinView) {
	for _, b := range builtins {
		path := b.Executable
		e.executable[b.Name] = &path
	}
	e.copied = ""
}

func (e *builtinEditor) field(name string) *string {
	if e.executable[name] == nil {
		e.executable[name] = new(string)
	}
	return e.executable[name]
}

func builtinByID(s app.Snapshot, id string) (app.BuiltinView, bool) {
	i := slices.IndexFunc(s.Builtins, func(b app.BuiltinView) bool { return b.ID() == id })
	if i < 0 {
		return app.BuiltinView{}, false
	}
	return s.Builtins[i], true
}

// agentChoices are the agents that can be chosen: built-ins once they've been found,
// then saved ones. Groups appear only when there are both kinds to tell apart.
func (v *View) agentChoices(s app.Snapshot) []choice {
	choices := []choice{}
	for _, b := range s.Builtins {
		if b.Offered() {
			choices = append(choices, choice{ID: b.ID(), Name: b.Title, Group: "Built-in", Description: "Mu's built-in adapter", Note: builtinNote(b)})
		}
	}
	grouped := len(choices) > 0
	for _, a := range s.Agents {
		item := choice{ID: a.ID, Name: a.Name, Description: a.Command}
		if grouped {
			item.Group = "Custom"
		}
		choices = append(choices, item)
	}
	return choices
}

// builtinNote marks a menu row that needs attention, and nothing otherwise.
func builtinNote(b app.BuiltinView) string {
	if b.Status.State == app.BuiltinReady {
		return ""
	}
	return builtinStateLabel(b)
}

func builtinStateLabel(b app.BuiltinView) string {
	switch b.Status.State {
	case app.BuiltinReady:
		return "Ready"
	case app.BuiltinNotFound:
		return "Not found"
	case app.BuiltinSignedOut:
		return "Not signed in"
	case app.BuiltinOutdated:
		return "Needs update"
	case app.BuiltinBroken:
		return "Not working"
	}
	return "Checking…"
}

func builtinStatusText(b app.BuiltinView) string {
	return joinNonEmpty(" · ", builtinStateLabel(b), versionLabel(b.Status.Version))
}

func versionLabel(version string) string {
	if version == "" {
		return ""
	}
	return "version " + version
}

func builtinStatusColor(t *ui.Theme, b app.BuiltinView) ui.Color {
	switch b.Status.State {
	case app.BuiltinReady:
		return okColor(t)
	case app.BuiltinUnknown:
		return t.TextMuted
	}
	return warnColor(t)
}

// keepAgentAvailable moves off an agent that can't be chosen any more, such as a
// built-in that's no longer found, and onto one that appears when there was none.
func (v *View) keepAgentAvailable(s app.Snapshot) {
	if !slices.ContainsFunc(v.agentChoices(s), func(c choice) bool { return c.ID == v.agentID }) {
		v.agentID = s.DefaultAgentID
	}
}

// builtinOpen reports whether a built-in's details take the place of the custom
// agent form. One that wasn't found always shows its details under its row.
func (v *View) builtinOpen(s app.Snapshot) bool {
	b, ok := builtinByID(s, v.editAgent.ID)
	return ok && b.Offered()
}

// mark stands in for an agent's logo.
func mark(c *ui.Context, text string, dim bool) {
	t := c.Theme()
	box := ui.Box(c).Size(26, 26).Radius(6).Shrink(0).Center().Background(t.Text.Alpha(0.07))
	if dim {
		box.Background(t.Text.Alpha(0.035))
	}
	box.Children(func() {
		label := ui.Text(c, text).FontSize(13).FontWeight(600)
		if dim {
			label.TextColor(faint(t))
		}
	})
}

func agentsCaption(c *ui.Context, title, hint string) {
	t := c.Theme()
	ui.Row(c).Gap(8).Margin(4, 0, -6, 2).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(500).TextColor(t.TextMuted).SingleLine().Shrink(0)
		ui.Text(c, hint).FontSize(11.5).TextColor(faint(t)).SingleLine().Shrink(1).MinWidth(0).Tooltip(hint)
	})
}

// builtinAgents is the Built-in section of Settings.
func (v *View) builtinAgents(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	agentsCaption(c, "Built-in", "Mu includes the adapter. You only need the agent itself.")
	ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
		for _, b := range s.Builtins {
			if b.Offered() {
				v.builtinRow(c, s, b)
			} else {
				v.builtinNotFound(c, b)
			}
		}
		for _, later := range comingSoon {
			ui.Row(c.Key("later-"+later.name)).Height(40).Padding(0, 14, 0, 11).Gap(10).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				mark(c, later.mark, true)
				ui.Text(c, later.name).FontSize(13).TextColor(faint(t)).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, "Coming soon").FontSize(11.5).TextColor(faint(t)).SingleLine()
			})
		}
	})
}

func builtinIdentity(c *ui.Context, b app.BuiltinView, status func()) {
	mark(c, b.Mark, false)
	ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
		ui.Text(c, b.Title).FontSize(13).FontWeight(500).SingleLine()
		status()
	})
}

func (v *View) builtinRow(c *ui.Context, s app.Snapshot, b app.BuiltinView) {
	t := c.Theme()
	open := v.editAgent.ID == b.ID()
	row := ui.ButtonBase(c.Key(b.ID())).Transition(colorMotion).Label("Agent: "+b.Title).FillWidth().Padding(9, 14, 9, 11).Gap(10)
	row.Background(rowColor(t, open, row.Hovered()))
	row.Children(func() {
		builtinIdentity(c, b, func() { builtinStatus(c, b) })
		if s.DefaultAgentID == b.ID() {
			chip(c, "Default", t.TextMuted)
		}
		chevron := ui.Icon(c, chevronIcon).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
		if open {
			chevron.Rotate(180)
		}
	}).OnClick(func() {
		v.editAgent, v.makeDefault = state.AgentConfig{ID: b.ID()}, false
		if open {
			v.editAgent = state.AgentConfig{}
		}
		v.settingsNotice, v.err = "", ""
	})
	if open {
		v.builtinPanel(c, s, b)
	}
}

func builtinStatus(c *ui.Context, b app.BuiltinView) {
	t := c.Theme()
	ui.Row(c).Gap(6).Children(func() {
		if b.Checking {
			ui.Spinner(c).Size(10, 10).Label("Checking " + b.Title)
		} else {
			dot(c, builtinStatusColor(t, b)).Size(6, 6)
		}
		ui.Text(c, builtinStatusText(b)).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
	})
}

func panelColumn(c *ui.Context, b app.BuiltinView) ui.Element {
	t := c.Theme()
	return ui.Column(c.Key("panel-"+b.Name)).Padding(12, 14, 14, 14).Gap(14).Background(t.Surface).BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
}

// builtinPanel opens under a found built-in's row. There is no name or command to
// fill in, and changes apply at once.
func (v *View) builtinPanel(c *ui.Context, s app.Snapshot, b app.BuiltinView) {
	t := c.Theme()
	panelColumn(c, b).Children(func() {
		v.builtinWarning(c, b, false) // The field below has its own Check again.
		ui.Column(c).Gap(9).Children(func() {
			for _, fact := range b.Facts {
				ui.Row(c.Key(fact.Label)).Gap(12).AlignItems(ui.Start).Children(func() {
					ui.Text(c, fact.Label).FontSize(12).TextColor(t.TextMuted).Width(70).Shrink(0)
					ui.Text(c, fact.Text).FontSize(12).Grow(1).MinWidth(0)
				})
			}
			quietAction(c, "Import "+b.Title+" sessions", "Import "+b.Title+" sessions", !b.Usable()).AlignSelf(ui.Start).Margin(0, 0, 0, 75).OnClick(func() {
				v.openImports()
				v.agentID = b.ID()
			})
		})
		v.executableField(c, b, "Leave empty to use "+b.Name+" from your PATH.")
		isDefault := s.DefaultAgentID == b.ID()
		wanted := isDefault
		ui.Checkbox(c, &wanted, "Default for new threads").Disabled(isDefault)
		if wanted != isDefault {
			v.fail(v.app.SetDefaultAgent(b.ID()))
		}
	})
}

// builtinNotFound is a built-in's place in Settings when it wasn't found. It stays
// open, because here is the only way to say where it is if it isn't on the PATH.
func (v *View) builtinNotFound(c *ui.Context, b app.BuiltinView) {
	t := c.Theme()
	ui.Row(c.Key(b.ID())).Padding(9, 14, 9, 11).Gap(10).Children(func() {
		builtinIdentity(c, b, func() { ui.Text(c, builtinStateLabel(b)).FontSize(11.5).TextColor(t.TextMuted).SingleLine() })
	})
	panelColumn(c, b).Children(func() {
		ui.Row(c).Gap(4).Children(func() {
			ui.Text(c, "Mu couldn't find "+b.Title+". Get it from").FontSize(12).SingleLine()
			v.builtinLink(c, b)
		})
		v.executableField(c, b, "Already have it? Enter its path if it isn't on your PATH.")
	})
}

// executableField is the one setting a built-in has. Checking saves it.
func (v *View) executableField(c *ui.Context, b app.BuiltinView, hint string) {
	field(c, b.Title+" executable", hint, func() {
		ui.Row(c).Gap(8).Children(func() {
			placeholder := shortPath(b.Status.Path)
			if placeholder == "" {
				placeholder = "/path/to/" + b.Name
			}
			ui.TextInput(c.Key("executable-"+b.Name), v.builtins.field(b.Name)).Label(b.Title + " executable").Placeholder(placeholder).Grow(1).MinWidth(120)
			v.checkAgain(c, b)
		})
	})
}

// checkAgain asks for a new check. That happens in the background, so this returns
// at once, and the result arrives with a later frame.
func (v *View) checkAgain(c *ui.Context, b app.BuiltinView) {
	ui.Button(c, "Check again").Shrink(0).Disabled(b.Checking).OnClick(func() {
		v.builtins.copied = ""
		v.fail(v.app.CheckBuiltin(b.Name, *v.builtins.field(b.Name)))
	})
}

// builtinLink opens the site that says how to get a built-in. Mu doesn't prescribe
// how to install anything.
func (v *View) builtinLink(c *ui.Context, b app.BuiltinView) {
	t := c.Theme()
	link := ui.ButtonBase(c).Transition(colorMotion).Role(ui.RoleLink).Label("Open " + b.SiteLabel).Tooltip(b.Site).Cursor(ui.CursorPointer).Radius(3).Shrink(0)
	color := t.Focus
	if link.Hovered() {
		color = t.Text
	}
	link.Children(func() { ui.Text(c, b.SiteLabel).FontSize(12).FontWeight(500).TextColor(color).SingleLine() }).OnClick(func() {
		c.OpenURLThen(b.Site, func(err error) {
			if err != nil {
				v.fail(err)
			}
		})
	})
}

// builtinStatusLine stands where a saved agent shows its command in the new-thread
// form, or the warning when it can't run yet.
func (v *View) builtinStatusLine(c *ui.Context, b app.BuiltinView) {
	t := c.Theme()
	switch b.Status.State {
	case app.BuiltinReady, app.BuiltinUnknown:
		ui.Row(c).Gap(6).Children(func() {
			if b.Checking {
				ui.Spinner(c).Size(10, 10).Label("Checking " + b.Title)
			} else {
				dot(c, builtinStatusColor(t, b)).Size(6, 6)
			}
			ui.Text(c, joinNonEmpty(" · ", builtinStatusText(b), "built-in adapter")).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
		})
	default:
		v.builtinWarning(c, b, true)
	}
}

// builtinWarning says what to do when a built-in that was found can't run yet.
func (v *View) builtinWarning(c *ui.Context, b app.BuiltinView, withCheck bool) {
	var title, body string
	switch b.Status.State {
	case app.BuiltinSignedOut:
		title, body = b.Title+" isn't signed in", b.SignInHelp
	case app.BuiltinOutdated:
		title = b.Title + " needs an update"
		body = "This is " + b.Title + " " + b.Status.Version + ". Mu needs " + b.MinimumVersion + " or newer. Update it, then check again."
	case app.BuiltinBroken:
		title, body = b.Title+" isn't working", b.Status.Detail
	default:
		return
	}
	t := c.Theme()
	warn := warnColor(t)
	ui.Column(c.Key("warning-"+b.Name)).Padding(10, 10, 10, 12).Gap(8).Radius(6).Border(1, warn.Alpha(0.3)).Background(warn.Alpha(0.06)).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Icon(c, warningIcon).Size(14, 14).TextColor(warn).Shrink(0)
			ui.Text(c, title).FontSize(13).FontWeight(500)
		})
		ui.Text(c, strings.TrimSpace(body)).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(6).Children(func() {
			if b.Status.State == app.BuiltinSignedOut && b.SignInCommand != "" {
				v.copyableCommand(c, b.SignInCommand)
			} else if withCheck {
				ui.Spacer(c)
			}
			if b.Status.State == app.BuiltinOutdated {
				v.builtinLink(c, b)
			}
			if withCheck {
				v.checkAgain(c, b)
			}
		})
	})
}

func (v *View) copyableCommand(c *ui.Context, command string) {
	t := c.Theme()
	ui.Text(c, command).Selectable().Font("monospace").FontSize(12).Grow(1).MinWidth(0).Padding(5, 8).Radius(5).Border(1, t.Border).Background(t.Background)
	label := "Copy"
	if v.builtins.copied == command {
		label = "Copied"
	}
	ui.Button(c, label).Label("Copy command").Shrink(0).OnClick(func() {
		c.Services().WriteClipboard(command)
		v.builtins.copied = command
	})
}

// builtinsNotFound stands under the command form when there's no agent to choose,
// saying where each built-in that wasn't found can be had.
func (v *View) builtinsNotFound(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	for _, b := range s.Builtins {
		if b.Offered() {
			continue
		}
		ui.Column(c.Key("not-found-" + b.Name)).Gap(4).Children(func() {
			ui.Row(c).Gap(4).Wrap().Children(func() {
				ui.Text(c, "Mu couldn't find "+b.Title+". Get it from").FontSize(12).TextColor(t.TextMuted).SingleLine()
				v.builtinLink(c, b)
				ui.Text(c, "or enter any ACP agent's command above.").FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			quietAction(c, "Check again", "Check again", b.Checking).AlignSelf(ui.Start).Margin(0, 0, 0, -7).OnClick(func() {
				v.fail(v.app.CheckBuiltin(b.Name, *v.builtins.field(b.Name)))
			})
		})
	}
}
