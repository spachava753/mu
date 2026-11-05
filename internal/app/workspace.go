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
	// Asking GitHub costs a network request, so it is much rarer, and otherwise
	// follows the local reading: a turn's end, a new thread in view, a new branch.
	pullRequestRefresh = time.Minute
	pullRequestTimeout = 10 * time.Second
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

// wakePullRequest asks GitHub now about the focused thread's branch.
func (a *App) wakePullRequest() {
	select {
	case a.pullRequestWake <- struct{}{}:
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
			if a.readWorkspace() {
				a.wakePullRequest()
			}
		case <-a.workspaceWake:
			a.readWorkspace()
			a.wakePullRequest()
		}
		a.refreshFocusedWorktrees()
	}
}

// watchPullRequest keeps the focused thread's pull request current, apart from
// the local readings so that a slow network cannot hold them up.
func (a *App) watchPullRequest() {
	defer a.wg.Done()
	tick := time.NewTicker(pullRequestRefresh)
	defer tick.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-tick.C:
		case <-a.pullRequestWake:
		}
		a.readPullRequest()
	}
}

func (a *App) focusedDirLocked() string {
	for _, t := range a.data.Threads {
		if t.ID == a.focused {
			return t.Cwd
		}
	}
	return ""
}

// readPullRequest asks about the focused folder's current branch. gh answers for
// whatever is checked out, so the answer only counts if the same branch is checked
// out before and after asking. Without a branch, gh has nothing to answer.
func (a *App) readPullRequest() {
	a.mu.Lock()
	dir := a.focusedDirLocked()
	a.mu.Unlock()
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, pullRequestTimeout)
	defer cancel()
	before, err := workspace.InspectGit(ctx, dir)
	if err != nil || before.Branch == "" {
		return
	}
	pr, err := workspace.InspectPullRequest(ctx, dir)
	if err != nil {
		return // Unknown: keep showing the last answer.
	}
	after, err := workspace.InspectGit(ctx, dir)
	if err != nil || after.Branch != before.Branch {
		return // The reading of the new branch asks again.
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := [2]string{dir, before.Branch}
	if a.pullRequests[key] != pr {
		a.pullRequests[key] = pr
		a.notifyLocked()
	}
}

// readWorkspace reads the focused folder and reports whether its branch changed.
func (a *App) readWorkspace() (changed bool) {
	a.mu.Lock()
	dir := a.focusedDirLocked()
	a.mu.Unlock()
	if dir == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(a.ctx, workspaceTimeout)
	defer cancel()
	git, err := workspace.InspectGit(ctx, dir)
	if err != nil {
		return false // Unknown: keep showing the last reading.
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.workspaces[dir] == git {
		return false
	}
	changed = a.workspaces[dir].Branch != git.Branch
	a.workspaces[dir] = git
	a.notifyLocked()
	return changed
}
