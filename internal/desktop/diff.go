package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/textdiff"
)

const (
	// Beyond this many lines a diff says how many it left out, since drawing every
	// line of a huge one on each frame would slow the whole window.
	maxDiffLines  = 400
	diffMaxHeight = 360
)

// Tool input and output scroll past these heights, so one long result can't push the
// rest of the conversation away.
const (
	toolInputHeight  = 200
	toolOutputHeight = 320
)

// toolBlock shows the text of a tool call, whichever tool it is.
func toolBlock(c *ui.Context, text string, height float32) {
	t := c.Theme()
	ui.Scroll(c).MaxHeight(height).Radius(6).Border(1, t.Border).Background(t.Surface).Padding(8, 10).Shrink(0).Children(func() {
		ui.Text(c, strings.TrimRight(text, "\n")).Selectable().Font("monospace").FontSize(12)
	})
}

// diffStat is the size of a change, for a tool call's collapsed row.
func diffStat(c *ui.Context, files []textdiff.File) {
	t := c.Theme()
	added, removed := 0, 0
	for _, f := range files {
		added += f.Added
		removed += f.Removed
	}
	ui.Row(c).Gap(5).Shrink(0).Children(func() {
		ui.Text(c, "+"+itoa(added)).FontSize(11.5).FontWeight(500).TextColor(okColor(t)).SingleLine()
		ui.Text(c, "−"+itoa(removed)).FontSize(11.5).FontWeight(500).TextColor(t.Danger).SingleLine()
	})
}

// diffs shows the changes to each file. They were computed before they got here, so
// this only draws. It knows nothing about which agent or tool made them.
func (v *View) diffs(c *ui.Context, key string, files []textdiff.File) {
	remaining := maxDiffLines
	for _, file := range files {
		ui.Column(c.Key("diff"+key+file.Path)).Radius(6).Border(1, c.Theme().Border).Clip().Shrink(0).Children(func() {
			diffHeader(c, file)
			ui.Scroll(c).Label("Changes to " + file.Path).MaxHeight(diffMaxHeight).Children(func() {
				remaining = diffLines(c, file, remaining)
			})
		})
	}
}

func diffHeader(c *ui.Context, file textdiff.File) {
	t := c.Theme()
	ui.Row(c).Padding(5, 10).Gap(8).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, shortPath(file.Path)).Font("monospace").FontSize(11.5).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0).Tooltip(file.Path)
		diffStat(c, []textdiff.File{file})
	})
}

// diffLines draws lines up to a limit shared by the files of one change, and returns
// what's left of it.
func diffLines(c *ui.Context, file textdiff.File, remaining int) int {
	t := c.Theme()
	hidden := 0
	for i, hunk := range file.Hunks {
		if remaining <= 0 {
			hidden += len(hunk)
			continue
		}
		if i > 0 {
			ui.Text(c, "⋯").Font("monospace").FontSize(11.5).TextColor(faint(t)).Padding(1, 10)
		}
		for _, line := range hunk {
			if remaining <= 0 {
				hidden++
				continue
			}
			remaining--
			diffLine(c, line)
		}
	}
	if hidden > 0 {
		ui.Text(c, itoa(hidden)+" more lines not shown").FontSize(11.5).TextColor(t.TextMuted).Padding(4, 10)
	}
	return remaining
}

func diffLine(c *ui.Context, line textdiff.Line) {
	t := c.Theme()
	sign, color, background := " ", t.TextMuted, ui.Transparent
	switch line.Op {
	case textdiff.Added:
		sign, color, background = "+", okColor(t), okColor(t).Alpha(0.12)
	case textdiff.Removed:
		sign, color, background = "−", t.Danger, t.Danger.Alpha(0.12)
	}
	ui.Row(c).AlignItems(ui.Start).Padding(0, 10, 0, 8).Gap(6).Background(background).Children(func() {
		ui.Text(c, sign).Font("monospace").FontSize(12).TextColor(color).Width(9).Shrink(0)
		text := ui.Text(c, line.Text).Selectable().Font("monospace").FontSize(12).Grow(1).MinWidth(0)
		if line.Op == textdiff.Context {
			text.TextColor(t.TextMuted)
		}
	})
}
