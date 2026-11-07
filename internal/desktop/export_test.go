package desktop

import "github.com/spachava753/mu/internal/media"

// DraftOfSelected is the draft in view, as the view holds it.
func (v *View) DraftOfSelected() string { return *v.draft(v.selected) }

// SetFileReader replaces how attached files are read, to hold a read open.
func (v *View) SetFileReader(read func(string) (*media.File, error)) { v.attach.read = read }
