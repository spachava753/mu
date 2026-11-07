package desktop

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

// quoteDelay is how long a selection must stay unchanged before it is offered.
// MyGo doesn't report when a drag ends, and this also covers selections made
// with the keyboard or by double-clicking.
const quoteDelay = 180 * time.Millisecond

// quotePopup follows the window's one text selection, when it is in an
// assistant message. Its place is relative to the message, so it scrolls along.
type quotePopup struct {
	owner, text       string
	since             time.Time
	placed, dismissed bool
	x, y              float32
}

// quoteAction offers to add the selection in an assistant message to the
// thread's draft. It's built inside the message, after its text.
func (v *View) quoteAction(c *ui.Context, thread app.ThreadView, entryID string, message ui.Element) {
	q := &v.quote
	owner := thread.ID + "\x00" + entryID
	text := quoteBlock(message.SelectedTexts())
	_, waiting := v.connecting[thread.ID] // A draft waiting to send is read-only.
	// The selection isn't shown once the focus leaves its message, and isn't
	// cleared by that: it would otherwise still be here, invisibly.
	if text == "" || thread.Archived || waiting || !message.FocusWithin() {
		if q.owner == owner {
			*q = quotePopup{}
		}
		return
	}
	if q.owner != owner || q.text != text {
		*q = quotePopup{owner: owner, text: text, since: c.Now()}
	}
	if q.dismissed {
		return
	}
	if wait := quoteDelay - c.Now().Sub(q.since); wait > 0 {
		c.After(wait)
		return
	}
	bounds := message.Bounds()
	if !q.placed {
		// Above the pointer, or the visible part of the message nearest it
		// when the keyboard made the selection.
		q.placed = true
		q.x, q.y, _ = message.PointerPosition()
		q.x = min(max(q.x, 0), bounds.W)
		q.y = min(max(q.y, v.messages.Y-bounds.Y, 0), v.messages.Y+v.messages.H-bounds.Y, bounds.H)
	}
	if y := bounds.Y + q.y; y < v.messages.Y || y > v.messages.Y+v.messages.H {
		return // Scrolled away with its text.
	}
	anchor := ui.Box(c.Key("quote-anchor")).Absolute().Left(q.x).Top(q.y).Size(0, 0)
	ui.Overlay(c, func() {
		t := c.Theme()
		button := ui.ButtonBase(c.Key("add-to-prompt")).Transition(popupMotion).Label("Add to prompt").AttachTo(anchor, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 10, 0).KeepFocus().Height(26).Padding(0, 10).Radius(6).Border(1, hairline(t))
		if button.Hovered() {
			button.Background(t.SurfaceHover)
		} else {
			button.Background(t.Surface)
		}
		button.Children(func() {
			ui.Text(c, "Add to prompt").FontSize(12).SingleLine()
		}).OnClick(func() { v.addQuote(thread.ID) })
		if button.OverlayShortcut(0, ui.KeyEscape) {
			q.dismissed = true
		}
	})
}

// addQuote adds the offered selection to the draft and returns to typing.
func (v *View) addQuote(threadID string) {
	draft := v.draft(threadID)
	*draft = appendQuote(*draft, v.quote.text)
	v.quote.dismissed = true
	v.picker.refocus = true
}

// quoteBlock is a selection as a Markdown quote, as it reads on screen: the
// paragraphs are kept apart, and their formatting isn't brought along.
func quoteBlock(parts []ui.SelectedText) string {
	var paragraphs []string
	for _, part := range parts {
		runes := []rune(part.Text)
		selected := string(runes[min(part.Start, len(runes)):min(part.End, len(runes))])
		// Only outer lines: a code line's indent is part of what was selected.
		if selected = strings.Trim(selected, "\r\n"); strings.TrimSpace(selected) == "" {
			continue
		}
		lines := strings.Split(selected, "\n")
		for i, line := range lines {
			if line = strings.TrimRight(line, " \t\r"); line == "" {
				lines[i] = ">"
			} else {
				lines[i] = "> " + line
			}
		}
		paragraphs = append(paragraphs, strings.Join(lines, "\n"))
	}
	return strings.Join(paragraphs, "\n>\n")
}

// appendQuote puts a quote on a new line after whatever is in the draft. It
// ends with a blank line: a line typed straight after a quote is part of it in
// Markdown, and the next quote stays a block of its own.
func appendQuote(draft, quote string) string {
	if draft != "" && !strings.HasSuffix(draft, "\n") {
		draft += "\n"
	}
	return draft + quote + "\n\n"
}
