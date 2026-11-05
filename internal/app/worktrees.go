package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/worktree"
)

const (
	worktreeReadTimeout   = 20 * time.Second
	worktreeRemoteTimeout = 45 * time.Second
	worktreePlanTimeout   = time.Minute
)

// ProjectWorktrees is what is known about a project's repository, as last read.
// Each reading replaces the value: snapshots share it, and it never changes.
type ProjectWorktrees struct {
	worktree.Listing
	PullRequests []worktree.PullRequest
	Error        string // the last reading failed; the rest is from before it

	unfetched  []worktree.Branch
	remoteHead string // the default remote's default branch, as it last said
	byPath     map[string]worktree.Worktree
	dir        string
}

// lookup finds the worktree a thread works in, in a subfolder or not, unless that is
// the project's own checkout. It runs for every thread in every snapshot, so it only
// reads a map.
func (p *ProjectWorktrees) lookup(cwd string) worktree.Worktree {
	if p == nil {
		return worktree.Worktree{}
	}
	for dir := filepath.Clean(cwd); dir != p.Root; dir = filepath.Dir(dir) {
		if w, ok := p.byPath[dir]; ok {
			return w
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return worktree.Worktree{}
}

// worktreeReader coalesces readings of one project's repository.
type worktreeReader struct {
	again, remote bool
}

// RefreshWorktrees reads a project's repository in the background; its result
// arrives in a later snapshot. With remote, it also asks the remotes and gh.
func (a *App) RefreshWorktrees(projectID string, remote bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshWorktreesLocked(projectID, remote)
}

func (a *App) refreshWorktreesLocked(projectID string, remote bool) {
	if a.closed || !slices.ContainsFunc(a.data.Projects, func(p state.Project) bool { return p.ID == projectID }) {
		return
	}
	if reader := a.worktreeReaders[projectID]; reader != nil {
		reader.again, reader.remote = true, reader.remote || remote
		return
	}
	a.worktreeReaders[projectID] = &worktreeReader{}
	a.wg.Add(1)
	go a.readWorktrees(projectID, remote)
}

// refreshFocusedWorktrees follows changes the agent or the user makes with git.
func (a *App) refreshFocusedWorktrees() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t := a.threadLocked(a.focused); t != nil && t.ProjectID != "" {
		a.refreshWorktreesLocked(t.ProjectID, false)
	}
}

// readWorktrees reads until no reading is wanted. Local refs are published first,
// as a remote can take a while to answer.
func (a *App) readWorktrees(projectID string, remote bool) {
	defer a.wg.Done()
	for {
		a.mu.Lock()
		i := slices.IndexFunc(a.data.Projects, func(p state.Project) bool { return p.ID == projectID })
		if i < 0 || a.closed {
			delete(a.worktreeReaders, projectID)
			a.mu.Unlock()
			return
		}
		dir := a.data.Projects[i].Dir
		a.mu.Unlock()

		ctx, cancel := context.WithTimeout(a.ctx, worktreeReadTimeout)
		listing, err := worktree.Read(ctx, dir)
		cancel()
		a.publishWorktrees(projectID, dir, listing, err)
		if remote && err == nil && listing.Repo() {
			ctx, cancel := context.WithTimeout(a.ctx, worktreeRemoteTimeout)
			read := worktree.ReadRemote(ctx, listing)
			cancel()
			a.publishRemote(projectID, dir, read)
		}

		a.mu.Lock()
		reader := a.worktreeReaders[projectID]
		if !reader.again {
			delete(a.worktreeReaders, projectID)
			a.mu.Unlock()
			return
		}
		remote = reader.remote
		reader.again, reader.remote = false, false
		a.mu.Unlock()
	}
}

func (a *App) publishWorktrees(projectID, dir string, listing worktree.Listing, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.worktrees[projectID]
	if err != nil {
		if a.ctx.Err() != nil {
			return
		}
		next := &ProjectWorktrees{Error: err.Error()}
		if old != nil {
			copied := *old
			copied.Error = err.Error()
			next = &copied
		}
		a.setWorktreesLocked(projectID, next)
		return
	}
	next := &ProjectWorktrees{Listing: listing, dir: dir, byPath: map[string]worktree.Worktree{}}
	for _, w := range listing.Worktrees {
		if w.Path != listing.Root {
			next.byPath[w.Path] = w
		}
	}
	if old != nil && old.dir == dir {
		next.PullRequests, next.unfetched, next.remoteHead = old.PullRequests, old.unfetched, old.remoteHead
	}
	next.mergeUnfetched()
	if old != nil && old.dir == dir && old.Error == "" && old.Root == next.Root && old.Prefix == next.Prefix && old.DefaultBase == next.DefaultBase && slices.Equal(old.Worktrees, next.Worktrees) && slices.Equal(old.Branches, next.Branches) && slices.Equal(old.Remotes, next.Remotes) {
		return // Nothing new: don't redraw every few seconds.
	}
	a.setWorktreesLocked(projectID, next)
}

// mergeUnfetched adds what only the remote knows: branches, unless a local ref now has
// them, and its default branch.
func (p *ProjectWorktrees) mergeUnfetched() {
	if p.remoteHead != "" && len(p.Remotes) > 0 {
		p.DefaultBase = worktree.DefaultRemote(p.Remotes) + "/" + p.remoteHead
	}
	known := map[[2]string]bool{}
	for _, b := range p.Branches {
		known[[2]string{b.Remote, b.Name}] = true
	}
	unfetched := slices.DeleteFunc(slices.Clone(p.unfetched), func(b worktree.Branch) bool { return known[[2]string{b.Remote, b.Name}] })
	p.Branches = append(slices.Clone(p.Branches), unfetched...)
}

func (a *App) publishRemote(projectID, dir string, read worktree.Remote) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.worktrees[projectID]
	if old == nil || old.dir != dir || a.ctx.Err() != nil {
		return
	}
	next := *old
	next.PullRequests, next.unfetched, next.remoteHead = read.PullRequests, read.Unfetched, read.Head
	next.Branches = slices.DeleteFunc(slices.Clone(old.Branches), func(b worktree.Branch) bool { return b.Unfetched })
	next.mergeUnfetched()
	a.setWorktreesLocked(projectID, &next)
}

// setWorktreesLocked replaces a project's reading; nil forgets it. Snapshots hold
// the map, so it is replaced too. A preview of the old state is out of date.
func (a *App) setWorktreesLocked(projectID string, value *ProjectWorktrees) {
	worktrees := maps.Clone(a.worktrees)
	if value == nil {
		delete(worktrees, projectID)
	} else {
		worktrees[projectID] = value
	}
	a.worktrees = worktrees
	if a.preview.ProjectID == projectID && a.preview.Target != (worktree.Target{}) {
		a.requestPreviewLocked(projectID, a.preview.Target)
	}
	a.notifyLocked()
}

func (a *App) projectLocked(id string) (state.Project, bool) {
	for _, p := range a.data.Projects {
		if p.ID == id {
			return p, true
		}
	}
	return state.Project{}, false
}

func (a *App) defaultWorktreeTemplateLocked() string {
	return filepath.Join(a.workingDirectoryLocked(), "worktrees", "{repo}", "{branch}")
}

func (a *App) worktreeTemplateLocked() string {
	if a.data.WorktreeTemplate != "" {
		return a.data.WorktreeTemplate
	}
	return a.defaultWorktreeTemplateLocked()
}

func (a *App) worktreeOptionsLocked(projectID string) worktree.Options {
	o := worktree.Options{Template: a.worktreeTemplateLocked()}
	if p := a.worktrees[projectID]; p != nil {
		o.RemoteHead = p.remoteHead
	}
	return o
}

// SetWorktreeTemplate chooses where new worktrees go. Empty restores the default,
// and existing worktrees stay where they are.
func (a *App) SetWorktreeTemplate(template string) error {
	template = strings.TrimSpace(template)
	if template != "" {
		if err := worktree.ValidateTemplate(template); err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errAppClosing
	}
	old := a.data.WorktreeTemplate
	a.data.WorktreeTemplate = template
	if err := a.saveLocked(); err != nil {
		a.data.WorktreeTemplate = old
		return err
	}
	if a.preview.Target != (worktree.Target{}) {
		a.requestPreviewLocked(a.preview.ProjectID, a.preview.Target)
	}
	return nil
}

// WorktreePreview is what starting a thread on a target would do.
type WorktreePreview struct {
	ProjectID string
	Target    worktree.Target
	Done      bool
	Plan      worktree.Plan
	Error     string
}

type previewState struct {
	WorktreePreview
	generation uint64
	running    bool
}

// PreviewWorktree works out a target's plan in the background; it arrives in
// a later snapshot. Asking again for the same target does nothing.
func (a *App) PreviewWorktree(projectID string, target worktree.Target) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preview.ProjectID != projectID || a.preview.Target != target {
		a.requestPreviewLocked(projectID, target)
	}
}

// requestPreviewLocked keeps what the preview said until a newer answer for the same
// target replaces it, and runs one plan at a time, the latest.
func (a *App) requestPreviewLocked(projectID string, target worktree.Target) {
	if a.closed {
		return
	}
	if a.preview.ProjectID != projectID || a.preview.Target != target {
		a.preview.WorktreePreview = WorktreePreview{ProjectID: projectID, Target: target}
	} else {
		a.preview.Done = a.preview.Done && a.preview.Error == ""
	}
	a.preview.generation++
	a.notifyLocked()
	if !a.preview.running {
		a.preview.running = true
		a.wg.Add(1)
		go a.runPreviews()
	}
}

func (a *App) runPreviews() {
	defer a.wg.Done()
	for {
		a.mu.Lock()
		generation, want := a.preview.generation, a.preview.WorktreePreview
		project, ok := a.projectLocked(want.ProjectID)
		options := a.worktreeOptionsLocked(want.ProjectID)
		if a.closed || want.Target == (worktree.Target{}) {
			a.preview.running = false
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()

		var plan worktree.Plan
		err := errors.New("project not found")
		if ok {
			ctx, cancel := context.WithTimeout(a.ctx, worktreePlanTimeout)
			plan, err = worktree.Resolve(ctx, project.Dir, want.Target, options)
			cancel()
		}

		a.mu.Lock()
		if a.preview.generation != generation {
			a.mu.Unlock()
			continue
		}
		a.preview.Done, a.preview.Plan, a.preview.Error = true, plan, ""
		if err != nil {
			a.preview.Error = err.Error()
		}
		a.preview.running = false
		a.notifyLocked()
		a.mu.Unlock()
		return
	}
}

// ClearWorktreePreview stops following a target when the form moves on.
func (a *App) ClearWorktreePreview() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.preview.WorktreePreview = WorktreePreview{}
	a.preview.generation++
}

// WorktreeOperation is a worktree being created or deleted in the background, until
// the view has collected how it went with ForgetWorktreeOperation.
type WorktreeOperation struct {
	ID        string
	ProjectID string
	Label     string // the branch or worktree it is about
	Deleting  bool
	Path      string // the worktree, when deleting
	Done      bool
	ThreadID  string   // the new thread, when creating
	Archived  []string // the threads that were in it, when deleting
	Notes     []string
	Error     string
}

func (a *App) worktreeOperationsLocked() []WorktreeOperation {
	if len(a.worktreeOps) == 0 {
		return nil
	}
	operations := make([]WorktreeOperation, len(a.worktreeOps))
	for i, op := range a.worktreeOps {
		operations[i] = *op
	}
	return operations
}

// ForgetWorktreeOperation drops a finished operation.
func (a *App) ForgetWorktreeOperation(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.worktreeOps = slices.DeleteFunc(a.worktreeOps, func(op *WorktreeOperation) bool { return op.ID == id && op.Done })
}

// worktreeTurn is a place in line for a change that could race for a folder or a ref,
// even from projects that share a repository or a folder name. Turns run in the order
// they were taken, and each sees the state the one before it left.
type worktreeTurn struct {
	before <-chan struct{}
	done   chan struct{}
	once   sync.Once
}

func (a *App) takeWorktreeTurnLocked() *worktreeTurn {
	t := &worktreeTurn{before: a.worktreeTail, done: make(chan struct{})}
	a.worktreeTail = t.done
	return t
}

// wait returns when it is this turn's go. Turns before it always end.
func (t *worktreeTurn) wait() {
	if t.before != nil {
		<-t.before
	}
}

func (t *worktreeTurn) end() { t.once.Do(func() { close(t.done) }) }

// startWorktreeOperationLocked runs work in the background.
func (a *App) startWorktreeOperationLocked(op *WorktreeOperation, work func(ctx context.Context) error) {
	op.ID = state.ID()
	a.worktreeOps = append(a.worktreeOps, op)
	a.notifyLocked()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		err := work(a.ctx)
		a.mu.Lock()
		defer a.mu.Unlock()
		op.Done = true
		if err != nil {
			op.Error = err.Error()
		}
		a.refreshWorktreesLocked(op.ProjectID, false)
		a.notifyLocked()
	}()
}

// CreateInWorktree starts a thread on a target in a project's repository: in a new
// worktree, or one that exists. Its agent is a saved or built-in one if agentID is
// set, and otherwise a one-off command. It returns an operation at once; the thread
// exists when that is done. If a later step fails, a worktree already made stays:
// Mu deletes them only when asked, and it can be picked under Worktrees.
//
// A remote that stalls holds up only this operation: fetching comes before its turn,
// and readying the worktree after. Attaching has neither, and takes its turn now, so
// that it stays ahead of a deletion requested after it.
func (a *App) CreateInWorktree(agentID, command, projectID string, target worktree.Target) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errAppClosing
	}
	name := ""
	if agentID != "" {
		config, ok := a.agentConfigLocked(agentID)
		if !ok {
			return "", errors.New("choose a configured agent in Settings")
		}
		command, name = config.Command, config.Name
	} else if _, err := agent.ParseCommand(command); err != nil {
		return "", err
	}
	if _, ok := a.projectLocked(projectID); !ok {
		return "", errors.New("project not found")
	}
	var turn *worktreeTurn
	if target.Kind == worktree.KindPath {
		turn = a.takeWorktreeTurnLocked()
	}
	op := &WorktreeOperation{ProjectID: projectID, Label: targetLabel(target)}
	a.startWorktreeOperationLocked(op, func(ctx context.Context) error {
		defer func() {
			if turn != nil {
				turn.end()
			}
		}()
		if turn != nil {
			turn.wait()
		}
		a.mu.Lock()
		project, ok := a.projectLocked(projectID)
		options := a.worktreeOptionsLocked(projectID)
		listing := a.worktrees[projectID]
		a.mu.Unlock()
		if !ok {
			return errors.New("project not found")
		}
		// Planned again here: things may have changed since the preview, and the
		// remote's default branch may not have been read for it yet.
		var movedDefault string
		if target.Kind == worktree.KindNew && target.Base == "" && listing != nil {
			if head := worktree.RemoteHead(ctx, listing.Listing); head != "" {
				options.RemoteHead, movedDefault = head, listing.DefaultBase
			}
		}
		plan, err := worktree.Resolve(ctx, project.Dir, target, options)
		if err != nil {
			return err
		}
		notes, err := worktree.Prepare(ctx, plan)
		if err != nil {
			return err
		}
		if movedDefault != "" && movedDefault != plan.StartLabel {
			notes = append(notes, "Started from "+plan.StartLabel+", the remote's default branch now, not "+movedDefault+".")
		}
		if turn == nil {
			a.mu.Lock()
			turn = a.takeWorktreeTurnLocked()
			a.mu.Unlock()
			turn.wait()
		}
		plan, err = worktree.Add(ctx, plan)
		if err != nil {
			return err
		}
		if plan.Action != worktree.ActionAttach {
			// Its turn ends here, so it needs protecting from deletion while it is
			// readied. Attaching holds its turn until the thread exists.
			a.mu.Lock()
			op.Path = plan.Path
			a.mu.Unlock()
			turn.end()
		}
		notes = append(append(notes, plan.Warnings...), worktree.Finish(ctx, plan)...)

		a.mu.Lock()
		defer a.mu.Unlock()
		left := ""
		if plan.Action != worktree.ActionAttach {
			left = " The worktree stays at " + plan.Path + "."
		}
		if a.closed {
			return errors.New("app is closing." + left)
		}
		if _, ok := a.projectLocked(projectID); !ok {
			return errors.New("the project was removed." + left)
		}
		id, err := a.addThreadLocked(command, agentID, name, projectID, plan.WorkDir())
		if err != nil {
			return fmt.Errorf("%w.%s", err, left)
		}
		op.ThreadID, op.Notes = id, notes
		return nil
	})
	return op.ID, nil
}

func targetLabel(t worktree.Target) string {
	switch t.Kind {
	case worktree.KindPullRequest:
		return fmt.Sprintf("PR #%d", t.Number)
	case worktree.KindPath:
		return filepath.Base(t.Path)
	}
	return t.Branch
}

// checkFolderLocked refuses a new use of a folder whose worktree is being deleted.
func (a *App) checkFolderLocked(dir string) error {
	if len(a.deletingFolders) == 0 {
		return nil
	}
	real := realPath(dir)
	for _, deleting := range a.deletingFolders {
		if worktree.Contains(deleting, real) {
			return fmt.Errorf("the worktree at %s is being deleted", deleting)
		}
	}
	return nil
}

// DeleteWorktree removes a linked worktree of a project's repository, in the
// background, with git worktree remove and never its force for uncommitted work.
// If it can be removed, the sessions of the threads in it close first, and those
// threads are archived, as their folder is gone. With deleteBranch, its branch is
// deleted too if fully merged. Any problem leaves the threads as they were.
func (a *App) DeleteWorktree(projectID, path string, deleteBranch bool) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errAppClosing
	}
	target, source, _, err := a.checkDeleteLocked(projectID, path)
	if err != nil {
		return "", err
	}
	op := &WorktreeOperation{ProjectID: projectID, Label: target.Name(), Deleting: true, Path: path}
	a.startWorktreeOperationLocked(op, func(ctx context.Context) error {
		// A remote a submodule asks can stall, so this comes before its turn, to hold up
		// only this operation. It runs again in its turn, without asking.
		check := &worktree.RemovalCheck{}
		if err := check.Run(ctx, source, path); err != nil {
			return err
		}
		a.mu.Lock()
		turn := a.takeWorktreeTurnLocked()
		a.mu.Unlock()
		defer turn.end()
		turn.wait()
		// Again: an operation ahead in the queue may have added a thread to it.
		a.mu.Lock()
		target, source, affected, err := a.checkDeleteLocked(projectID, path)
		if err != nil {
			a.mu.Unlock()
			return err
		}
		for _, id := range affected {
			r := a.runtimeLocked(id)
			r.configuring, r.deletingWorktree = true, true
		}
		// From here nothing new may start using the folder.
		real := realPath(path)
		a.deletingFolders = append(a.deletingFolders, real)
		a.notifyLocked()
		a.mu.Unlock()
		err = a.removeWorktree(ctx, check, source, target, affected, op, deleteBranch)
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, id := range affected {
			if r := a.runtimes[id]; r != nil {
				r.configuring, r.deletingWorktree = false, false
			}
		}
		a.deletingFolders = slices.DeleteFunc(a.deletingFolders, func(dir string) bool { return dir == real })
		return err
	})
	return op.ID, nil
}

// realPath resolves symlinks, as a folder may be saved under another name for it.
func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// checkDeleteLocked finds a worktree that may be deleted, its repository's checkout
// to run git in, and the threads in it.
func (a *App) checkDeleteLocked(projectID, path string) (target worktree.Worktree, source string, affected []string, err error) {
	_, ok := a.projectLocked(projectID)
	listing := a.worktrees[projectID]
	if !ok || listing == nil {
		return target, "", nil, errors.New("project not found")
	}
	i := slices.IndexFunc(listing.Worktrees, func(w worktree.Worktree) bool { return w.Path == path })
	if i < 0 {
		return target, "", nil, errors.New("that worktree is no longer listed")
	}
	target = listing.Worktrees[i]
	if target.Main {
		return target, "", nil, errors.New("this is the repository's primary checkout, which can't be deleted")
	}
	if path == listing.Root {
		return target, "", nil, errors.New("this is the project's own folder, which Mu never deletes")
	}
	for _, op := range a.worktreeOps {
		if !op.Deleting && !op.Done && op.Path == path {
			return target, "", nil, errors.New("this worktree is still being set up")
		}
	}
	real := realPath(path)
	for _, p := range a.data.Projects {
		if worktree.Contains(real, realPath(p.Dir)) {
			return target, "", nil, fmt.Errorf("project %s uses this folder; remove the project first", p.Name)
		}
	}
	for _, t := range a.data.Threads {
		if !worktree.Contains(real, realPath(t.Cwd)) {
			continue
		}
		if a.threadLocked(t.ID).Running || a.configuringLocked(t.ID) {
			return target, "", nil, errors.New("this worktree has threads at work; stop them or let them finish first")
		}
		affected = append(affected, t.ID)
	}
	return target, listing.Root, affected, nil
}

// folderClose is a session Mu has asked an agent to close, and the folder that agent
// may be using until it has, whatever has become of the thread. A close that succeeds
// ends this. One the agent refused lasts until a later close of that session succeeds
// or the process has exited.
type folderClose struct {
	conn    *connection
	session string
	cwd     string
	done    chan struct{}
	refusal string
}

// trackFolderCloseLocked records a close being sent, in place of any earlier one for
// that session.
func (a *App) trackFolderCloseLocked(c *connection, session, cwd string, done chan struct{}) *folderClose {
	a.folderCloses = slices.DeleteFunc(a.folderCloses, func(f *folderClose) bool { return f.conn == c && f.session == session })
	closing := &folderClose{conn: c, session: session, cwd: cwd, done: done}
	a.folderCloses = append(a.folderCloses, closing)
	return closing
}

// trackUnsettledLocked records a request to start a session that Mu stopped waiting for.
// It has no close to wait for, and might never answer, so it counts as a refusal until
// it settles or the process has exited.
func (a *App) trackUnsettledLocked(c *connection, cwd string) *folderClose {
	done := make(chan struct{})
	close(done)
	unsettled := &folderClose{conn: c, cwd: cwd, done: done, refusal: "it hasn't answered a request to start a session there"}
	a.folderCloses = append(a.folderCloses, unsettled)
	return unsettled
}

// finishFolderCloseLocked records how a close went. A nil close was never tracked.
func (a *App) finishFolderCloseLocked(closing *folderClose, err error) {
	if closing == nil {
		return
	}
	if err != nil {
		closing.refusal = err.Error()
		return
	}
	a.folderCloses = slices.DeleteFunc(a.folderCloses, func(f *folderClose) bool { return f == closing })
}

// closeInFolder asks the agent to close a session no thread is attached to, tracked
// like any other. It blocks on the agent, so it can't hold the mutex.
func (a *App) closeInFolder(c *connection, session, cwd string, deadline time.Duration) error {
	done := make(chan struct{})
	defer close(done)
	a.mu.Lock()
	closing := a.trackFolderCloseLocked(c, session, cwd, done)
	a.mu.Unlock()
	err := c.process.CloseSession(session, deadline)
	a.mu.Lock()
	a.finishFolderCloseLocked(closing, err)
	a.mu.Unlock()
	return err
}

// folderClosesLocked finds what matters about a folder: closes under way, and refusals
// by agents still running. It forgets refusals of agents that have exited.
func (a *App) folderClosesLocked(folder string) []*folderClose {
	a.folderCloses = slices.DeleteFunc(a.folderCloses, func(f *folderClose) bool {
		return f.refusal != "" && f.conn.process.Exited()
	})
	var found []*folderClose
	for _, f := range a.folderCloses {
		if worktree.Contains(folder, realPath(f.cwd)) {
			found = append(found, f)
		}
	}
	return found
}

func (a *App) removeWorktree(ctx context.Context, check *worktree.RemovalCheck, source string, target worktree.Worktree, affected []string, op *WorktreeOperation, deleteBranch bool) error {
	if err := check.Run(ctx, source, target.Path); err != nil {
		return err
	}
	// Close their sessions and let the agents finish with the folder, along with any
	// other close under way there, whatever has become of its thread.
	real := realPath(target.Path)
	a.mu.Lock()
	for _, id := range affected {
		if r := a.runtimes[id]; r != nil {
			a.detachLocked(r)
		}
	}
	closes := a.folderClosesLocked(real)
	a.notifyLocked()
	a.mu.Unlock()
	for _, f := range closes {
		select {
		case <-f.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a.mu.Lock()
	refusal := ""
	for _, f := range a.folderClosesLocked(real) {
		if f.refusal != "" {
			refusal = f.refusal
		}
	}
	a.mu.Unlock()
	if refusal != "" {
		return errors.New("nothing was deleted: an agent wouldn't close its session in this worktree, and is still running: " + refusal)
	}
	if err := check.Remove(ctx, source, target.Path); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.data.Threads {
		if t := &a.data.Threads[i]; slices.Contains(affected, t.ID) && !t.Archived {
			t.Archived = true
			op.Archived = append(op.Archived, t.ID)
		}
	}
	delete(a.workspaces, target.Path)
	if err := a.saveLocked(); err != nil {
		// Their folder is gone either way. Saving is retried with the next change.
		op.Notes = append(op.Notes, "The worktree was deleted, but archiving its threads could not be saved: "+err.Error())
	}
	if deleteBranch && target.Branch != "" {
		a.mu.Unlock()
		err := worktree.DeleteBranch(ctx, source, target.Branch)
		a.mu.Lock()
		if err != nil {
			op.Notes = append(op.Notes, "Kept branch "+target.Branch+": "+err.Error())
		} else {
			op.Notes = append(op.Notes, "Deleted branch "+target.Branch+".")
		}
	}
	return nil
}
