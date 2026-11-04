package app

import (
	"context"
	"time"

	"github.com/spachava753/mu/internal/workspace"
)

const (
	// A checkout can happen outside any turn, so the focused folder is read again
	// regularly. Reading it costs a few short git commands.
	workspaceRefresh = 5 * time.Second
	workspaceTimeout = 3 * time.Second
)

// FocusThread names the thread the user is looking at, whose folder the app
// keeps reading. An empty ID names none.
func (a *App) FocusThread(id string) {
	a.mu.Lock()
	changed := a.focused != id
	a.focused = id
	a.mu.Unlock()
	if changed {
		a.wakeWorkspace()
	}
}

// wakeWorkspace reads the focused folder now, as after a turn that may have
// changed it.
func (a *App) wakeWorkspace() {
	select {
	case a.workspaceWake <- struct{}{}:
	default:
	}
}

// watchWorkspace reads the focused thread's folder, one reading at a time and
// outside the mutex, until the app closes.
func (a *App) watchWorkspace() {
	defer a.wg.Done()
	tick := time.NewTicker(workspaceRefresh)
	defer tick.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-tick.C:
		case <-a.workspaceWake:
		}
		a.readWorkspace()
	}
}

func (a *App) readWorkspace() {
	a.mu.Lock()
	dir := ""
	for _, t := range a.data.Threads {
		if t.ID == a.focused {
			dir = t.Cwd
		}
	}
	a.mu.Unlock()
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, workspaceTimeout)
	defer cancel()
	git, err := workspace.InspectGit(ctx, dir)
	if err != nil {
		return // Unknown: keep showing the last reading.
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspaces[dir] != git {
		a.workspaces[dir] = git
		a.notifyLocked()
	}
}
