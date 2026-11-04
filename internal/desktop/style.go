package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Layout constants shared by the native view and its tests.
const (
	sidebarWidth = 232
	railWidth    = 48
	readingWidth = 800
)

// The look follows Linear's published approach: a near-monochrome theme
// with low chroma, hairline borders, layered neutral surfaces, compact
// medium-weight type, and the accent reserved for focus, selection and the
// one primary action. The content area is an inset panel on darker chrome.
// Surfaces stay opaque and matte; focus keeps a distinct, accessible outline.
func matte(c *ui.Context) {
	t := *c.Theme()
	t.Radius, t.Spacing, t.FontSize = 6, 3.5, 13
	if !c.Preferences().HighContrast {
		if t.Dark {
			t.Background, t.Surface = ui.Hex("#141517"), ui.Hex("#1a1b1e")
			t.SurfaceHover, t.SurfacePressed, t.Border = ui.Hex("#1f2023"), ui.Hex("#25262a"), ui.Hex("#27282c")
			t.Text, t.TextMuted = ui.Hex("#e3e4e8"), ui.Hex("#8b8e96")
			t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = ui.Hex("#5e6ad2"), ui.Hex("#6b77dc"), ui.Hex("#5560c4"), ui.Hex("#ffffff")
			t.Focus, t.Selection = ui.Hex("#7170ff"), ui.Hex("#2c2f5a")
			t.Danger = ui.Hex("#eb5757")
		} else {
			t.Background, t.Surface = ui.Hex("#ffffff"), ui.Hex("#fbfbfc")
			t.SurfaceHover, t.SurfacePressed, t.Border = ui.Hex("#f1f2f4"), ui.Hex("#e9eaed"), ui.Hex("#e4e5e9")
			t.Text, t.TextMuted = ui.Hex("#1f2023"), ui.Hex("#6b6f76")
			t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = ui.Hex("#5e6ad2"), ui.Hex("#5561c7"), ui.Hex("#4c57b8"), ui.Hex("#ffffff")
			t.Focus, t.Selection = ui.Hex("#5e6ad2"), ui.Hex("#dfe1f7")
			t.Danger = ui.Hex("#d93f3f")
		}
		t.Inverse, t.InverseText = t.SurfacePressed, t.Text
	}
	c.SetTheme(&t)
	c.Root().Background(chrome(c))
}

// chrome is the darker frame around the content panel: the sidebar and the
// window edges.
func chrome(c *ui.Context) ui.Color {
	t := c.Theme()
	if c.Preferences().HighContrast {
		return t.Background
	}
	if t.Dark {
		return ui.Hex("#0d0e10")
	}
	return ui.Hex("#f4f5f8")
}

// faint is the quietest text: timestamps, counts and hints.
func faint(t *ui.Theme) ui.Color { return t.TextMuted.Mix(t.Background, 0.3) }

// hairline is a border a step stronger than the theme's, for controls.
func hairline(t *ui.Theme) ui.Color { return t.Border.Mix(t.Text, 0.06) }

// Status colors mark state only; text stays in the theme's text colors.
func okColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#4cb782")
	}
	return ui.Hex("#2f9e6a")
}
func warnColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#f2c94c")
	}
	return ui.Hex("#c48a00")
}

// markdownTheme sizes agent prose for reading and keeps headings modest.
func markdownTheme(t *ui.Theme) *markdown.Theme {
	th := &markdown.Theme{FontSize: 14, LineHeight: 1.55, StrongWeight: 600, CodeBlockBackground: t.Surface, CodeBlockBorder: t.Border, CodeBlockRadius: 6}
	sizes := [6]float32{1.3, 1.18, 1.07, 1, 0.95, 0.9}
	for i := range th.Headings {
		th.Headings[i] = markdown.HeadingStyle{Size: sizes[i], Weight: 600, Color: t.Text, Top: 1.2, Bottom: 0.45}
	}
	return th
}

func svg(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
}

var (
	folderPlusIcon = svg(`<path d="M3.5 18V6.5c0-.8.7-1.5 1.5-1.5h4l2 2.5h8c.8 0 1.5.7 1.5 1.5V18c0 .8-.7 1.5-1.5 1.5H5c-.8 0-1.5-.7-1.5-1.5zM12 11v5M9.5 13.5h5"/>`)
	panelIcon      = svg(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>`)
	plusIcon       = svg(`<path d="M12 5v14M5 12h14"/>`)
	slidersIcon    = svg(`<path d="M4 7h16M4 17h16"/><circle cx="9" cy="7" r="2.5"/><circle cx="15" cy="17" r="2.5"/>`)
	gearIcon       = svg(`<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`)
	importIcon     = svg(`<path d="M12 4v11M7.5 10.5 12 15l4.5-4.5M5 20h14"/>`)
	forkIcon       = svg(`<circle cx="6" cy="5.5" r="2.2"/><circle cx="18" cy="5.5" r="2.2"/><circle cx="12" cy="18.5" r="2.2"/><path d="M6 7.7v1.8c0 1.4 1.1 2.5 2.5 2.5h7c1.4 0 2.5-1.1 2.5-2.5V7.7M12 12v4.3"/>`)
	archiveIcon    = svg(`<rect x="3" y="4" width="18" height="4.5" rx="1"/><path d="M5 8.5V18c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V8.5M10 12.5h4"/>`)
	restoreIcon    = svg(`<rect x="3" y="4" width="18" height="4.5" rx="1"/><path d="M5 8.5V18c0 1.1.9 2 2 2h2.5M19 8.5V18c0 1.1-.9 2-2 2h-2.5M9 15l3-3 3 3M12 12v8"/>`)
	moreIcon       = svg(`<circle cx="5.5" cy="12" r="1.1"/><circle cx="12" cy="12" r="1.1"/><circle cx="18.5" cy="12" r="1.1"/>`)
	closeIcon      = svg(`<path d="M6.5 6.5l11 11M17.5 6.5l-11 11"/>`)
	sendIcon       = svg(`<path d="M12 19V5.5M6 11.5l6-6 6 6"/>`)
	stopIcon       = svg(`<rect x="7" y="7" width="10" height="10" rx="1.5"/>`)
	checkIcon      = svg(`<path d="m5.5 12.5 4 4 9-9.5"/>`)
	chevronIcon    = svg(`<path d="m8 10 4 4 4-4"/>`)
	disclosure     = svg(`<path d="m9.5 6.5 5.5 5.5-5.5 5.5"/>`)
	toolIcon       = svg(`<path d="m5 16.5 4.5-4.5L5 7.5M12 17h7"/>`)
	thoughtIcon    = svg(`<path d="M9.5 18h5M10.5 21h3M12 3.5c-3.3 0-5.5 2.5-5.5 5.4 0 1.9 1 3.2 2 4.1.6.6 1 1.3 1 2.1V15h5v.1c0-.8.4-1.5 1-2.1 1-.9 2-2.2 2-4.1 0-2.9-2.2-5.4-5.5-5.4z"/>`)
	planIcon       = svg(`<path d="m4 6.5 1.6 1.6L8.5 5M4 16.5l1.6 1.6 2.9-3.1M12 7h8M12 12h8M12 17h8"/>`)
	warningIcon    = svg(`<path d="M12 4 3 19.5h18L12 4z"/><path d="M12 10v4.5M12 17.2v.1"/>`)
)

// iconButton is a borderless, square toolbar button. Its label names it for
// assistive technology and tests, and doubles as its tooltip.
func iconButton(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	return iconAction(c, label, icon, false)
}

func iconAction(c *ui.Context, label string, icon *ui.SVG, disabled bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).Tooltip(label).Size(26, 26).Radius(5).Shrink(0).Disabled(disabled)
	hovered := b.Hovered() && !disabled
	if hovered {
		b.Background(t.Text.Alpha(0.06))
	}
	b.Children(func() {
		icon := ui.Icon(c, icon).Size(15, 15).TextColor(t.TextMuted)
		switch {
		case disabled:
			icon.Opacity(0.35)
		case hovered:
			icon.TextColor(t.Text)
		}
	})
	return b
}

// smallIconButton is an iconButton for a dense row, such as a section heading.
func smallIconButton(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).Tooltip(label).Size(20, 20).Radius(4).Shrink(0)
	hovered := b.Hovered()
	if hovered {
		b.Background(t.Text.Alpha(0.06))
	}
	b.Children(func() {
		i := ui.Icon(c, icon).Size(13, 13).TextColor(t.TextMuted)
		if hovered {
			i.TextColor(t.Text)
		}
	})
	return b
}

// navRow is a full-width sidebar destination with an icon.
func navRow(c *ui.Context, label string, icon *ui.SVG, active bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).FillWidth().Height(28).Padding(0, 8).Radius(5).Gap(9)
	b.Background(sideColor(t, active, b.Hovered()))
	b.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(t.TextMuted)
		text := ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
		if active || b.Hovered() {
			text.TextColor(t.Text)
		}
	})
	return b
}

// quietButton is a compact text button without a border.
func quietButton(c *ui.Context, text string) ui.Element { return quietAction(c, text, text, false) }

// quietAction is a quietButton whose accessible label can say more than its
// text, for when the row around it is short of room.
func quietAction(c *ui.Context, text, label string, disabled bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).Padding(3, 7).Radius(5).Shrink(0).Disabled(disabled)
	color := t.TextMuted
	switch {
	case disabled:
		color = faint(t)
	case b.Hovered():
		b.Background(t.Text.Alpha(0.06))
		color = t.Text
	}
	b.Children(func() { ui.Text(c, text).FontSize(12).FontWeight(500).TextColor(color).SingleLine() })
	return b
}

// sideColor is the background of a row on the chrome: transparent until
// hovered, a neutral layer when selected. The accent is not used here.
func sideColor(t *ui.Theme, selected, hovered bool) ui.Color {
	switch {
	case selected && hovered:
		return t.Text.Alpha(0.1)
	case selected:
		return t.Text.Alpha(0.075)
	case hovered:
		return t.Text.Alpha(0.045)
	}
	return ui.Transparent
}

// markedColor is the background of a sidebar row marked for a bulk action,
// distinct from the open thread's neutral layer.
func markedColor(t *ui.Theme, hovered bool) ui.Color {
	if hovered {
		return t.Accent.Alpha(0.24)
	}
	return t.Accent.Alpha(0.17)
}

// rowColor is the background of a row inside the content panel.
func rowColor(t *ui.Theme, selected, hovered bool) ui.Color {
	switch {
	case selected && hovered:
		return t.Text.Alpha(0.075)
	case selected:
		return t.Text.Alpha(0.055)
	case hovered:
		return t.Text.Alpha(0.035)
	}
	return ui.Transparent
}

// chip is a small outlined label. A color other than the muted text color
// adds a status dot.
func chip(c *ui.Context, text string, color ui.Color) ui.Element {
	t := c.Theme()
	return ui.Row(c).Shrink(0).Gap(5).Padding(1, 6).Radius(4).Border(1, hairline(t)).Children(func() {
		if color != t.TextMuted {
			dot(c, color).Size(6, 6)
		}
		ui.Text(c, text).FontSize(11).FontWeight(500).TextColor(t.TextMuted).SingleLine()
	})
}

func dot(c *ui.Context, color ui.Color) ui.Element {
	return ui.Box(c).Size(7, 7).Radius(4).Background(color).Shrink(0)
}

// smallDisclosure is a compact collapsible for secondary protocol details.
func smallDisclosure(c *ui.Context, label string, open *bool, fn func()) {
	t := c.Theme()
	ui.Column(c).Children(func() {
		p := ui.CollapsibleBase(c, open)
		trigger := p.Trigger.Transition(colorMotion).Label(label).AlignSelf(ui.Start).Padding(2, 8, 2, 2).Radius(5).Gap(5)
		if trigger.Hovered() {
			trigger.Background(t.Text.Alpha(0.05))
		}
		trigger.Children(func() {
			ui.Icon(c, disclosure).Size(11, 11).TextColor(faint(t)).Rotate(90 * p.Progress())
			ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
		p.Panel(fn).Padding(4, 0, 2, 18)
	})
}

// detailText shows raw protocol data as selectable monospace text.
func detailText(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	return ui.Text(c, text).Selectable().Font("monospace").FontSize(11.5).TextColor(t.TextMuted).Padding(8, 10).Radius(6).Background(t.Surface).Border(1, t.Border)
}

// field groups a label, its control and an optional hint.
func field(c *ui.Context, label, hint string, control func()) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500)
		control()
		if hint != "" {
			ui.Text(c, hint).FontSize(12).TextColor(c.Theme().TextMuted)
		}
	})
}

// screenHeader is the compact one-line title bar shared by every screen.
func screenHeader(c *ui.Context, title, subtitle string, actions func()) {
	t := c.Theme()
	ui.Row(c).Height(44).Shrink(0).Padding(0, 10, 0, 20).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).Margin(0, 8, 0, 0).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(500).SingleLine().Shrink(0).MaxWidthPercent(70)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12.5).TextColor(faint(t)).SingleLine().Shrink(3).MinWidth(0).Tooltip(subtitle)
			}
		})
		if actions != nil {
			actions()
		}
	})
}

// shortPath abbreviates the home directory; tooltips keep the full path.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}

func timeLabel(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	at, now := at.Local(), time.Now()
	switch {
	case at.Year() == now.Year() && at.YearDay() == now.YearDay():
		return at.Format("15:04")
	case at.Year() == now.Year():
		return at.Format("Jan 2")
	}
	return at.Format("Jan 2, 2006")
}

func joinNonEmpty(sep string, parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

type choice struct{ ID, Name, Description, Group string }

// Keep protocol IDs separate from labels, including duplicate labels and groups.
func selectChoice(c *ui.Context, label string, value string, options []choice, disabled bool, changed func(string)) {
	selectControl(c, label, "", value, options, disabled, changed)
}

// chipSelect is a compact select for toolbars: the field name, muted, then
// the current value.
func chipSelect(c *ui.Context, label, field string, value string, options []choice, disabled bool, changed func(string)) {
	selectControl(c, label, field, value, options, disabled, changed)
}

func selectControl(c *ui.Context, label, field string, value string, options []choice, disabled bool, changed func(string)) {
	t := c.Theme()
	compact := field != ""
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
	trigger := s.Trigger.Transition(colorMotion).Label(label).Disabled(disabled).MinWidth(0)
	if compact {
		trigger.Height(24).Padding(0, 5, 0, 7).Radius(5).Gap(5).Shrink(1).MaxWidth(220).Tooltip(field + ": " + name)
		if trigger.Hovered() && !disabled {
			trigger.Background(t.Text.Alpha(0.06))
		}
		trigger.Children(func() {
			ui.Text(c, field).FontSize(12).TextColor(faint(t)).SingleLine().Shrink(0)
			value := ui.Text(c, name).FontSize(12).FontWeight(500).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
			if disabled {
				value.TextColor(t.TextMuted)
			}
			ui.Icon(c, chevronIcon).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
		})
	} else {
		trigger.FillWidth().Padding(6, 10).Radius(6).Border(1, hairline(t)).Background(t.Surface)
		if trigger.Hovered() && !disabled {
			trigger.BorderColor(hairline(t).Mix(t.Text, 0.1))
		}
		trigger.Children(func() {
			ui.Text(c, name).SingleLine().Grow(1).MinWidth(0)
			ui.Icon(c, chevronIcon).Size(14, 14).TextColor(t.TextMuted)
		})
	}
	s.Popup(func(panel ui.Element) {
		bounds := s.Trigger.Bounds()
		_, height := c.Size()
		// SelectBase supplies keyboard navigation, but unlike Select it does
		// not supply a scroller. Bound it to the larger side of the trigger.
		room := max(bounds.Y, height-bounds.Y-bounds.H) - 16
		width := bounds.W
		if compact {
			width = max(width, 220)
		}
		panel.Transition(popupMotion).Width(width).Padding(4).Radius(8).Border(1, hairline(t)).Background(t.Surface)
		ui.Scroll(c).Label(label + " choices").MaxHeight(min(300, max(48, room))).Children(func() {
			group := ""
			for _, o := range options {
				if o.Group != "" && o.Group != group {
					ui.Text(c, o.Group).Padding(8, 8, 4, 8).FontSize(11.5).FontWeight(500).TextColor(faint(t))
					group = o.Group
				}
				item := s.Item(o.ID).Transition(colorMotion).Label(o.Name).Height(30).Padding(0, 8).Radius(5).Gap(8).Tooltip(o.Description)
				if item.Highlighted() {
					item.Background(t.Text.Alpha(0.06))
				}
				item.Children(func() {
					ui.Text(c, o.Name).SingleLine().Grow(1).MinWidth(0)
					check := ui.Icon(c, checkIcon).Size(14, 14).TextColor(t.TextMuted)
					if o.ID != value {
						check.Opacity(0)
					}
				})
			}
		})
	})
	if selected != value {
		changed(selected)
	}
}
