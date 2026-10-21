package desktop

import "github.com/egoist/mygo/ui"

// Matte, opaque surfaces follow the desktop's light/dark preference. No glass,
// gradients or shadows; focus keeps a distinct, accessible outline.
func matte(c *ui.Context) {
	t := *c.Theme()
	t.Radius, t.Spacing = 6, 3.5
	if !c.Preferences().HighContrast {
		if t.Dark {
			t.Background, t.Surface = ui.Hex("#18191c"), ui.Hex("#202125")
			t.SurfaceHover, t.SurfacePressed, t.Border = ui.Hex("#292a30"), ui.Hex("#343640"), ui.Hex("#33343b")
			t.Text, t.TextMuted = ui.Hex("#eeeef1"), ui.Hex("#a5a6b0")
			t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = ui.Hex("#dcdde3"), ui.Hex("#f0f0f4"), ui.Hex("#bfc1cc"), ui.Hex("#18191c")
			t.Focus, t.Selection = ui.Hex("#a4afff"), ui.Hex("#3c4160")
			t.Danger = ui.Hex("#f58c8c")
		} else {
			t.Background, t.Surface = ui.Hex("#fafafa"), ui.Hex("#f1f1f3")
			t.SurfaceHover, t.SurfacePressed, t.Border = ui.Hex("#e8e8ed"), ui.Hex("#dcdde5"), ui.Hex("#d4d4dc")
			t.Text, t.TextMuted = ui.Hex("#202126"), ui.Hex("#62636f")
			t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = ui.Hex("#303139"), ui.Hex("#444651"), ui.Hex("#202126"), ui.Hex("#fafafa")
			t.Focus, t.Selection = ui.Hex("#5264b8"), ui.Hex("#dce0f7")
			t.Danger = ui.Hex("#b42318")
		}
		t.Inverse, t.InverseText = t.SurfacePressed, t.Text
	}
	c.SetTheme(&t)
	c.Root().Background(t.Background)
}

func svg(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
}

var (
	panelIcon    = svg(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>`)
	plusIcon     = svg(`<path d="M12 5v14M5 12h14"/>`)
	settingsIcon = svg(`<path d="M4 7h16M4 17h16"/><circle cx="9" cy="7" r="3"/><circle cx="15" cy="17" r="3"/>`)
	chevronIcon  = svg(`<path d="m8 10 4 4 4-4"/>`)
)

func iconButton(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).Tooltip(label).Size(30, 30).Radius(5)
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, icon).Size(17, 17).TextColor(c.Theme().TextMuted) })
	return b
}

func rowColor(t *ui.Theme, selected, hovered bool) ui.Color {
	if selected {
		if hovered {
			return t.SurfacePressed.Mix(t.Text, 0.06)
		}
		return t.SurfacePressed
	}
	if hovered {
		return t.SurfaceHover
	}
	return t.Surface
}

type choice struct{ ID, Name, Description, Group string }

// Keep protocol IDs separate from labels, including duplicate labels and groups.
func selectChoice(c *ui.Context, label string, value string, options []choice, disabled bool, changed func(string)) {
	selected := value
	s := ui.SelectBase(c, &selected)
	name := value
	for _, o := range options {
		if o.ID == value {
			name = o.Name
			break
		}
	}
	if name == "" {
		name = "Choose…"
	}
	s.Trigger.Transition(colorMotion).Label(label).Disabled(disabled).FillWidth().MinWidth(0).Padding(7, 10).Radius(6).Border(1, c.Theme().Border).Background(c.Theme().Surface).Children(func() {
		ui.Text(c, name).SingleLine().Grow(1).MinWidth(0)
		ui.Icon(c, chevronIcon).Size(14, 14).TextColor(c.Theme().TextMuted)
	})
	s.Popup(func(panel ui.Element) {
		bounds := s.Trigger.Bounds()
		_, height := c.Size()
		// SelectBase supplies keyboard navigation, but unlike Select it does
		// not supply a scroller. Bound it to the larger side of the trigger.
		room := max(bounds.Y, height-bounds.Y-bounds.H) - 16
		panel.Transition(popupMotion).Width(bounds.W).Padding(4).Radius(6).Border(1, c.Theme().Border).Background(c.Theme().Background)
		ui.Scroll(c).Label(label + " choices").MaxHeight(min(300, max(48, room))).Children(func() {
			group := ""
			for _, o := range options {
				if o.Group != "" && o.Group != group {
					ui.Text(c, o.Group).Padding(8, 6, 4, 6).FontSize(11).TextColor(c.Theme().TextMuted)
					group = o.Group
				}
				item := s.Item(o.ID).Transition(colorMotion).Label(o.Name).Padding(7, 8).Radius(4).Tooltip(o.Description)
				if item.Highlighted() {
					item.Background(c.Theme().SurfacePressed)
				}
				item.Children(func() { ui.Text(c, o.Name).SingleLine().Grow(1).MinWidth(0) })
			}
		})
	})
	if selected != value {
		changed(selected)
	}
}
