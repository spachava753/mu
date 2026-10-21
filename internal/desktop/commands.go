package desktop

import (
	"strings"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

type commandPicker struct {
	text                                                  string
	index                                                 int
	open, dismissed, reveal, refocus, attemptedConnection bool
}

func slashQuery(text string) (string, bool) {
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \t\r\n") {
		return "", false
	}
	return strings.ToLower(strings.TrimPrefix(text, "/")), true
}

func (p *commandPicker) matches(text string, commands []acp.AvailableCommand) []acp.AvailableCommand {
	if text != p.text {
		p.text, p.index, p.dismissed, p.reveal = text, 0, false, true
	}
	query, ok := slashQuery(text)
	if !ok {
		p.open, p.attemptedConnection = false, false
		return nil
	}
	var matches []acp.AvailableCommand
	for _, command := range commands {
		if strings.Contains(strings.ToLower(command.Name), query) || strings.Contains(strings.ToLower(command.Description), query) {
			matches = append(matches, command)
		}
	}
	p.index = min(p.index, max(0, len(matches)-1))
	return matches
}

func (v *View) messageInput(c *ui.Context, t app.ThreadView) {
	p := &v.picker
	input := ui.TextArea(c.Key("message"), v.draft(t.ID)).Label("Message").Placeholder("Message the agent… / for commands").Lines(2, 6).Disabled(t.Running).OnTextKey(func(k ui.TextKey) bool {
		matches := p.matches(k.Text(), t.Session.Commands)
		_, slash := slashQuery(k.Text())
		if slash && !p.dismissed && k.Mods == 0 {
			switch k.Key {
			case ui.KeyEscape:
				p.dismissed, p.open = true, false
				return true
			case ui.KeyUp, ui.KeyDown:
				if len(matches) > 0 {
					step := 1
					if k.Key == ui.KeyUp {
						step = -1
					}
					p.index = (p.index + step + len(matches)) % len(matches)
					p.reveal = true
					return true
				}
			case ui.KeyEnter, ui.KeyTab:
				if len(matches) > 0 {
					k.SetText("/" + matches[p.index].Name + " ")
					p.dismissed, p.open, p.refocus = true, false, true
					return true // Complete only. Never send on the same Enter.
				}
			}
		}
		if k.Key == ui.KeyEnter && k.Mods == 0 && !t.Running {
			if !t.Configuring && v.send(t.ID, k.Text()) {
				k.SetText("")
			}
			return true
		}
		return false
	})
	if p.refocus {
		input.Focus().SetTextSelection(utf8.RuneCountInString(*v.draft(t.ID)), utf8.RuneCountInString(*v.draft(t.ID)))
		p.refocus = false
	}
	matches := p.matches(*v.draft(t.ID), t.Session.Commands)
	_, slash := slashQuery(*v.draft(t.ID))
	p.open = slash && !p.dismissed && !t.Running && (input.Focused() || p.open)
	if p.open {
		v.usageOpen = false
		if !t.Connected && !t.Configuring && !p.attemptedConnection {
			p.attemptedConnection = true
			v.fail(v.app.Connect(t.ID))
		}
	}
	wasOpen := p.open
	ui.PopoverBase(c.Key("slash-commands"), input, &p.open, func(panel ui.Element) {
		bounds := input.Bounds()
		panel.Transition(popupMotion).Label("Slash commands").AttachTo(input, ui.AnchorTopStart, ui.AnchorBottomStart).Width(bounds.W).Margin(0, 0, 6, 0).Padding(4).Radius(6).Border(1, c.Theme().Border).Background(c.Theme().Background)
		ui.Scroll(c).Label("Command choices").MaxHeight(min(240, max(64, bounds.Y-20))).Children(func() {
			if len(matches) == 0 {
				message := "No matching commands"
				if !t.Connected || t.Configuring {
					message = "Loading commands…"
				}
				if t.Connected && len(t.Session.Commands) == 0 {
					message = "This agent has not reported any commands."
				}
				if t.SessionError != "" {
					message = t.SessionError
				}
				ui.Text(c, message).Padding(10).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			for i, command := range matches {
				item := ui.ButtonBase(c.Key(command.Name)).Transition(colorMotion).Label("/"+command.Name).FillWidth().Padding(8, 10).Radius(4)
				if i == p.index {
					item.Background(c.Theme().SurfacePressed)
				} else if item.Hovered() {
					item.Background(c.Theme().SurfaceHover)
				}
				if i == p.index && p.reveal {
					item.ScrollIntoView()
				}
				item.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, "/"+command.Name).FontSize(13).Bold().SingleLine()
						ui.Text(c, command.Description).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine()
						if command.Input != nil && command.Input.Unstructured != nil {
							ui.Text(c, command.Input.Unstructured.Hint).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
						}
					})
				}).OnClick(func() {
					*v.draft(t.ID) = "/" + command.Name + " "
					p.open, p.dismissed, p.refocus = false, true, true
				})
			}
		})
		p.reveal = false
	})
	if wasOpen && !p.open {
		p.dismissed = true
	}
}
