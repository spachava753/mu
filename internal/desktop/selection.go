package desktop

import (
	"slices"

	"github.com/egoist/mygo/ui"
)

// threadSelection is the set of threads marked for a bulk action. It is
// separate from the open thread: marking a thread does not open it. order is
// the threads as the sidebar lists them.
type threadSelection struct {
	mode   bool // the user chose Select, so plain clicks mark threads too
	marked map[string]bool
	anchor string // where a Shift range starts
	reveal string // the row to keep in view after a click changes the layout
}

// active reports whether the sidebar is showing selection controls.
func (s *threadSelection) active() bool { return s.mode || len(s.marked) > 0 }

func (s *threadSelection) has(id string) bool { return s.marked[id] }

func (s *threadSelection) count() int { return len(s.marked) }

func (s *threadSelection) clear() { *s = threadSelection{} }

func (s *threadSelection) toggle(id string) {
	if s.marked == nil {
		s.marked = map[string]bool{}
	}
	if s.marked[id] {
		delete(s.marked, id)
	} else {
		s.marked[id] = true
	}
	s.anchor = id
}

// extend marks the threads from the anchor to id. Without an anchor, the range
// starts at fallback, the open thread, if it is listed.
func (s *threadSelection) extend(order []string, id, fallback string) {
	if s.marked == nil {
		s.marked = map[string]bool{}
	}
	anchor := s.anchor
	if !slices.Contains(order, anchor) {
		anchor = fallback
	}
	from, to := slices.Index(order, anchor), slices.Index(order, id)
	if to < 0 {
		return
	}
	if from < 0 {
		from = to
	}
	if from > to {
		from, to = to, from
	}
	for _, listed := range order[from : to+1] {
		s.marked[listed] = true
	}
	s.anchor = id
}

// click applies a click on a row and reports whether it should open the thread
// instead of changing the selection.
func (s *threadSelection) click(order []string, id, open string, mods ui.Modifiers) bool {
	switch {
	case mods&ui.Shift != 0:
		s.extend(order, id, open)
	case mods&ui.Cmd != 0 || s.active():
		s.toggle(id)
	default:
		s.anchor = id // A later Shift range starts at the thread just opened.
		return true
	}
	s.reveal = id
	return false
}

// ids returns the marked threads in sidebar order.
func (s *threadSelection) ids(order []string) []string {
	var ids []string
	for _, id := range order {
		if s.marked[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

// retain drops marks on threads the sidebar no longer lists.
func (s *threadSelection) retain(order []string) {
	for id := range s.marked {
		if !slices.Contains(order, id) {
			delete(s.marked, id)
		}
	}
}

// keep replaces the marks, as with threads a bulk action skipped.
func (s *threadSelection) keep(ids []string) {
	s.marked = map[string]bool{}
	for _, id := range ids {
		s.marked[id] = true
	}
}
