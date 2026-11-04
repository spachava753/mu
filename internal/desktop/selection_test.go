package desktop

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestThreadSelection(t *testing.T) {
	order := []string{"a", "b", "c", "d", "e"}
	var s threadSelection
	if s.active() {
		t.Fatal("a new selection is active")
	}
	// A plain click opens the thread and marks nothing.
	if !s.click(order, "b", "", 0) || s.count() != 0 {
		t.Fatal("plain click changed the selection")
	}
	// Cmd toggles, without opening.
	if s.click(order, "b", "a", ui.Cmd) || !s.has("b") || s.has("a") {
		t.Fatal("Cmd click did not mark only that thread")
	}
	// Shift extends from the last marked thread.
	if s.click(order, "d", "a", ui.Shift) {
		t.Fatal("Shift click opened")
	}
	if got := s.ids(order); !slices.Equal(got, []string{"b", "c", "d"}) {
		t.Fatal(got)
	}
	// While selecting, a plain click marks too; the last unmark ends it.
	if s.click(order, "c", "a", 0) || s.has("c") {
		t.Fatal("plain click while selecting did not toggle")
	}
	s.toggle("b")
	s.toggle("d")
	if s.active() {
		t.Fatal("empty selection stayed active without Select")
	}
	// Select keeps it active with nothing marked, so plain clicks mark.
	s.mode = true
	if !s.active() || s.click(order, "a", "", 0) || !s.has("a") {
		t.Fatal("Select mode")
	}
	s.toggle("a")
	if !s.active() {
		t.Fatal("Select mode ended with its last mark")
	}
	s.clear()
	if s.active() || s.count() != 0 {
		t.Fatal("clear")
	}
	// With no anchor, Shift starts at the open thread, in either direction.
	s.click(order, "b", "d", ui.Shift)
	if got := s.ids(order); !slices.Equal(got, []string{"b", "c", "d"}) {
		t.Fatal(got)
	}
	s.clear()
	// With no anchor or listed open thread, Shift marks just that thread.
	s.click(order, "c", "gone", ui.Shift)
	if got := s.ids(order); !slices.Equal(got, []string{"c"}) {
		t.Fatal(got)
	}
	// Threads no longer listed lose their marks, and an unlisted one is ignored.
	s.retain([]string{"a", "b"})
	if s.count() != 0 {
		t.Fatal("retain kept an unlisted thread")
	}
	s.extend(order, "unlisted", "a")
	if s.count() != 0 {
		t.Fatal("extended to an unlisted thread")
	}
	// Opening a thread moves the anchor, even after an emptied selection.
	s.clear()
	s.toggle("d")
	s.toggle("d")
	if !s.click(order, "a", "", 0) {
		t.Fatal("plain click did not open")
	}
	s.click(order, "b", "a", ui.Shift)
	if got := s.ids(order); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
	// keep replaces the marks.
	s.keep([]string{"e"})
	if got := s.ids(order); !slices.Equal(got, []string{"e"}) {
		t.Fatal(got)
	}
}
