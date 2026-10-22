package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Layout constants shared by the native view and its tests.
const (
	sidebarWidth = 232
	railWidth    = 48
	readingWidth = 800
)

// Matte, opaque surfaces follow the desktop's light/dark preference. No glass,
// gradients or shadows; focus keeps a distinct, accessible outline.
func matte(c *ui.Context) {
	t := *c.Theme()
	t.Radius, t.Spacing = 6, 3.5
	if !c.Preferences().HighContrast {
		if t.Dark {
			t.Background, t.Surface = ui.Hex("#17181b"), ui.Hex("#1d1e22")
			t.SurfaceHover, t.SurfacePressed, t.Border = ui.Hex("#26272c"), ui.Hex("#2f3036"), ui.Hex("#2c2d33")
			t.Text, t.TextMuted = ui.Hex("#e9eaee"), ui.Hex("#9c9ea8")
			t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = ui.Hex("#8cb4f5"), ui.Hex("#a4c4f8"), ui.Hex("#7aa3e6"), ui.Hex("#0f1420")
			t.Focus, t.Selection = ui.Hex("#8cb4f5"), ui.Hex("#2e3c58")
			t.Danger = ui.Hex("#f28b82")
		} else {
			t.Background, t.Surface = ui.Hex("#ffffff"), ui.Hex("#f6f7f9")
			t.SurfaceHover, t.SurfacePressed, t.Border = ui.Hex("#eceef2"), ui.Hex("#e2e5ea"), ui.Hex("#e0e2e7")
			t.Text, t.TextMuted = ui.Hex("#1d2026"), ui.Hex("#5f6671")
			t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = ui.Hex("#2f6bd8"), ui.Hex("#2860c4"), ui.Hex("#2152a8"), ui.Hex("#ffffff")
			t.Focus, t.Selection = ui.Hex("#2f6bd8"), ui.Hex("#d5e2fa")
			t.Danger = ui.Hex("#c5221f")
		}
		t.Inverse, t.InverseText = t.SurfacePressed, t.Text
	}
	c.SetTheme(&t)
	c.Root().Background(t.Background)
}

// Status colors complement the theme. They mark state only; text stays
// legible in the theme's own text colors.
func okColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#7fc796")
	}
	return ui.Hex("#1e7d3e")
}
func warnColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#e8c26b")
	}
	return ui.Hex("#a3670a")
}

// userBubble distinguishes the user's prompts from agent prose.
func userBubble(t *ui.Theme) ui.Color {
	if t.Dark {
		return t.Background.Mix(t.Accent, 0.11)
	}
	return t.Background.Mix(t.Accent, 0.07)
}

func svg(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
}

var (
	panelIcon   = svg(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>`)
	plusIcon    = svg(`<path d="M12 5v14M5 12h14"/>`)
	slidersIcon = svg(`<path d="M4 7h16M4 17h16"/><circle cx="9" cy="7" r="2.5"/><circle cx="15" cy="17" r="2.5"/>`)
	gearIcon    = svg(`<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`)
	importIcon  = svg(`<path d="M12 4v11M7.5 10.5 12 15l4.5-4.5M5 20h14"/>`)
	forkIcon    = svg(`<circle cx="6" cy="5.5" r="2.2"/><circle cx="18" cy="5.5" r="2.2"/><circle cx="12" cy="18.5" r="2.2"/><path d="M6 7.7v1.8c0 1.4 1.1 2.5 2.5 2.5h7c1.4 0 2.5-1.1 2.5-2.5V7.7M12 12v4.3"/>`)
	archiveIcon = svg(`<rect x="3" y="4" width="18" height="4.5" rx="1"/><path d="M5 8.5V18c0 1.1.9 2 2 2h10c1.1 0 2-.9 2-2V8.5M10 12.5h4"/>`)
	restoreIcon = svg(`<rect x="3" y="4" width="18" height="4.5" rx="1"/><path d="M5 8.5V18c0 1.1.9 2 2 2h2.5M19 8.5V18c0 1.1-.9 2-2 2h-2.5M9 15l3-3 3 3M12 12v8"/>`)
	moreIcon    = svg(`<circle cx="5.5" cy="12" r="1.1"/><circle cx="12" cy="12" r="1.1"/><circle cx="18.5" cy="12" r="1.1"/>`)
	closeIcon   = svg(`<path d="M6.5 6.5l11 11M17.5 6.5l-11 11"/>`)
	sendIcon    = svg(`<path d="M12 19V5.5M6 11.5l6-6 6 6"/>`)
	stopIcon    = svg(`<rect x="7" y="7" width="10" height="10" rx="1.5"/>`)
	checkIcon   = svg(`<path d="m5.5 12.5 4 4 9-9.5"/>`)
	chevronIcon = svg(`<path d="m8 10 4 4 4-4"/>`)
	disclosure  = svg(`<path d="m9.5 6.5 5.5 5.5-5.5 5.5"/>`)
	toolIcon    = svg(`<path d="m5 16.5 4.5-4.5L5 7.5M12 17h7"/>`)
	thoughtIcon = svg(`<path d="M9.5 18h5M10.5 21h3M12 3.5c-3.3 0-5.5 2.5-5.5 5.4 0 1.9 1 3.2 2 4.1.6.6 1 1.3 1 2.1V15h5v.1c0-.8.4-1.5 1-2.1 1-.9 2-2.2 2-4.1 0-2.9-2.2-5.4-5.5-5.4z"/>`)
	planIcon    = svg(`<path d="m4 6.5 1.6 1.6L8.5 5M4 16.5l1.6 1.6 2.9-3.1M12 7h8M12 12h8M12 17h8"/>`)
	warningIcon = svg(`<path d="M12 4 3 19.5h18L12 4z"/><path d="M12 10v4.5M12 17.2v.1"/>`)
)

// iconButton is a borderless, square toolbar button. Its label names it for
// assistive technology and tests, and doubles as its tooltip.
func iconButton(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	return iconAction(c, label, icon, false)
}

func iconAction(c *ui.Context, label string, icon *ui.SVG, disabled bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).Tooltip(label).Size(28, 28).Radius(6).Shrink(0).Disabled(disabled)
	if b.Hovered() && !disabled {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		icon := ui.Icon(c, icon).Size(16, 16).TextColor(t.TextMuted)
		if disabled {
			icon.Opacity(0.4)
		}
	})
	return b
}

// navRow is a full-width sidebar destination with an icon.
func navRow(c *ui.Context, label string, icon *ui.SVG, active bool) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(label).FillWidth().Padding(6, 8).Radius(6).Gap(9)
	b.Background(rowColor(t, active, b.Hovered()))
	b.Children(func() {
		ui.Icon(c, icon).Size(15, 15).TextColor(t.TextMuted)
		ui.Text(c, label).FontSize(12.5).SingleLine().Grow(1).MinWidth(0)
	})
	return b
}

// quietButton is a compact text button without a border.
func quietButton(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Transition(colorMotion).Label(text).Padding(3, 7).Radius(5).Shrink(0)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Text(c, text).FontSize(11.5).TextColor(t.TextMuted).SingleLine() })
	return b
}

func rowColor(t *ui.Theme, selected, hovered bool) ui.Color {
	if selected {
		base := t.Surface.Mix(t.Accent, 0.13)
		if hovered {
			return base.Mix(t.Accent, 0.05)
		}
		return base
	}
	if hovered {
		return t.SurfaceHover
	}
	return t.Surface
}

// chip is a small status label, tinted by color.
func chip(c *ui.Context, text string, color ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(10.5).FontWeight(600).TextColor(color).Padding(1, 6).Radius(4).Background(color.Alpha(0.13)).SingleLine().Shrink(0)
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
			trigger.Background(t.SurfaceHover)
		}
		trigger.Children(func() {
			ui.Icon(c, disclosure).Size(11, 11).TextColor(t.TextMuted).Rotate(90 * p.Progress())
			ui.Text(c, label).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
		})
		p.Panel(fn).Padding(4, 0, 2, 18)
	})
}

// detailText shows raw protocol data as selectable monospace text.
func detailText(c *ui.Context, text string) ui.Element {
	t := c.Theme()
	return ui.Text(c, text).Selectable().Font("monospace").FontSize(11).TextColor(t.TextMuted).Padding(8, 10).Radius(6).Background(t.Surface)
}

// field groups a label, its control and an optional hint.
func field(c *ui.Context, label, hint string, control func()) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(600)
		control()
		if hint != "" {
			ui.Text(c, hint).FontSize(11.5).TextColor(c.Theme().TextMuted)
		}
	})
}

// screenHeader is the compact title bar shared by the full-page screens.
func screenHeader(c *ui.Context, title, subtitle string, actions func()) {
	t := c.Theme()
	ui.Row(c).Height(52).Shrink(0).Padding(0, 16, 0, 24).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(14.5).FontWeight(600).SingleLine()
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Tooltip(subtitle)
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
		trigger.Padding(4, 6, 4, 8).Radius(6).Gap(5).Shrink(1).MaxWidth(220).Tooltip(field + ": " + name)
		if trigger.Hovered() && !disabled {
			trigger.Background(t.SurfaceHover)
		}
		trigger.Children(func() {
			ui.Text(c, field).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(0)
			value := ui.Text(c, name).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
			if disabled {
				value.TextColor(t.TextMuted)
			}
			ui.Icon(c, chevronIcon).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
		})
	} else {
		trigger.FillWidth().Padding(7, 10).Radius(6).Border(1, t.Border).Background(t.Background)
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
		panel.Transition(popupMotion).Width(width).Padding(4).Radius(8).Border(1, t.Border).Background(t.Background)
		ui.Scroll(c).Label(label + " choices").MaxHeight(min(300, max(48, room))).Children(func() {
			group := ""
			for _, o := range options {
				if o.Group != "" && o.Group != group {
					ui.Text(c, o.Group).Padding(8, 8, 4, 8).FontSize(11).FontWeight(600).TextColor(t.TextMuted)
					group = o.Group
				}
				item := s.Item(o.ID).Transition(colorMotion).Label(o.Name).Padding(6, 8).Radius(5).Gap(8).Tooltip(o.Description)
				if item.Highlighted() {
					item.Background(t.SurfaceHover)
				}
				item.Children(func() {
					ui.Text(c, o.Name).SingleLine().Grow(1).MinWidth(0)
					check := ui.Icon(c, checkIcon).Size(14, 14).TextColor(t.Accent)
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
