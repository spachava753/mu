package app

import (
	"errors"
	"slices"

	"github.com/spachava753/mu/internal/state"
)

// BulkResult reports what a bulk action did. Skipped threads are busy with a
// turn or a session request, and were left unchanged.
type BulkResult struct {
	Changed, Skipped []string
}

// ArchiveThreads archives or restores threads with one atomic save: if it
// fails, none change. Archiving closes their idle sessions. Threads that are
// gone or already in that state are ignored.
func (a *App) ArchiveThreads(ids []string, archive bool) (BulkResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return BulkResult{}, errors.New("app is closing")
	}
	var result BulkResult
	var changed []*thread
	for _, id := range ids {
		t := a.threadLocked(id)
		switch {
		case t == nil || t.Archived == archive:
		case t.Running || a.configuringLocked(id):
			result.Skipped = append(result.Skipped, id)
		default:
			t.Archived = archive
			changed = append(changed, t)
			result.Changed = append(result.Changed, id)
		}
	}
	if len(changed) == 0 {
		return result, nil
	}
	if err := a.saveLocked(); err != nil {
		for _, t := range changed {
			t.Archived = !archive
		}
		return BulkResult{}, err
	}
	if archive {
		for _, id := range result.Changed {
			if r := a.runtimes[id]; r != nil {
				a.detachLocked(r)
			}
		}
	}
	return result, nil
}

// DeleteThreads removes threads from Mu with one atomic save: if it fails,
// none are removed. As with Delete, only Mu's references go; agent sessions and
// workspace files are unchanged. Threads that are gone are ignored.
func (a *App) DeleteThreads(ids []string) (BulkResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return BulkResult{}, errors.New("app is closing")
	}
	var result BulkResult
	for _, id := range ids {
		t := a.threadLocked(id)
		switch {
		case t == nil:
		case t.Running || a.configuringLocked(id):
			result.Skipped = append(result.Skipped, id)
		default:
			result.Changed = append(result.Changed, id)
		}
	}
	if len(result.Changed) == 0 {
		return result, nil
	}
	old := a.data.Threads
	a.data.Threads = slices.DeleteFunc(slices.Clone(old), func(t state.Thread) bool { return slices.Contains(result.Changed, t.ID) })
	if err := a.saveLocked(); err != nil {
		a.data.Threads = old
		return BulkResult{}, err
	}
	for _, id := range result.Changed {
		if r := a.runtimes[id]; r != nil {
			a.detachLocked(r)
		}
		delete(a.runtimes, id)
	}
	return result, nil
}
