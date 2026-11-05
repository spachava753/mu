package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// fetch brings one ref: no tags, pruning or submodules. The only refs it overwrites are
// remote-tracking ones, which only ever mirror the remote.
func fetch(ctx context.Context, dir string, f Fetch) error {
	if strings.HasPrefix(f.Remote, "-") {
		return errors.New("invalid remote")
	}
	refspec := f.From
	if f.Into != "" {
		refspec = "+" + f.From + ":" + f.Into
	}
	_, err := git(ctx, fetchTimeout, dir, "fetch", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--", f.Remote, refspec)
	return err
}

// WorkDir is where a thread works in the plan's checkout: its folder, or the subfolder
// matching the project's, if the checkout has that.
func (p Plan) WorkDir() string {
	if p.Prefix != "" {
		sub := filepath.Join(p.Path, p.Prefix)
		if info, err := os.Stat(sub); err == nil && info.IsDir() {
			return sub
		}
	}
	return p.Path
}

// A plan is carried out in three steps. Prepare and Finish can wait on a remote for
// minutes, and touch nothing another worktree could be after. Add can race for a
// folder or a ref, and is local, so callers run Adds one at a time.
//
// None of them overwrites a folder or moves an existing branch except by a
// fast-forward, and each leaves what it made in place if a later one fails: Mu deletes
// worktrees only when the user asks.

// Prepare fetches what the plan starts from, and reports what the user may want to know.
func Prepare(ctx context.Context, p Plan) ([]string, error) {
	if p.Fetch == nil {
		return nil, nil
	}
	err := fetch(ctx, p.Source, *p.Fetch)
	if err == nil {
		return nil, nil
	}
	// A stale base is still a valid start for a new branch.
	if ctx.Err() != nil || p.Action != ActionNewBranch || p.Fetch.Into == "" || !refExists(ctx, p.Source, p.Fetch.Into) {
		return nil, fmt.Errorf("couldn't fetch %s from %s: %w", strings.TrimPrefix(p.Fetch.From, "refs/heads/"), p.Fetch.Remote, err)
	}
	return []string{fmt.Sprintf("Couldn't fetch %s from %s, so it started from the last fetched copy.", strings.TrimPrefix(p.Fetch.From, "refs/heads/"), p.Fetch.Remote)}, nil
}

// Add makes the worktree, and returns the plan with the folder it used, as the one
// planned may have been taken since. A plan to attach only checks the worktree is there.
func Add(ctx context.Context, p Plan) (Plan, error) {
	worktrees, err := list(ctx, p.Source)
	if err != nil {
		return p, err
	}
	if p.Action == ActionAttach {
		// It may have been deleted since it was planned.
		_, err := planPath(Listing{Worktrees: worktrees}, p.Path)
		return p, err
	}
	p.Path, err = destination(ctx, p.Template, Listing{Root: p.Source, Worktrees: worktrees}, p.Folder)
	if err != nil {
		return p, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return p, err
	}
	var args []string
	switch p.Action {
	case ActionNewBranch:
		args = []string{"worktree", "add", "--no-track", "-b", p.Branch, "--", p.Path, p.Start}
	case ActionTrackRemote:
		// Git's --track refuses a ref outside the fetch refspec, as in a shallow or
		// single-branch clone, so the tracking is configured below.
		args = []string{"worktree", "add", "--no-track", "-b", p.Branch, "--", p.Path, p.Start}
	case ActionLocalBranch:
		args = []string{"worktree", "add", "--", p.Path, p.Branch} // A full ref would detach HEAD.
	case ActionDetached:
		args = []string{"worktree", "add", "--detach", "--", p.Path, p.Start}
	default:
		return p, fmt.Errorf("unknown action %q", p.Action)
	}
	// Killed partway, git leaves a checkout that is locked, half-made and registered.
	settled, done := settling(ctx)
	defer done()
	if _, err := git(settled, checkoutTimeout, p.Source, args...); err != nil {
		if strings.TrimSpace(err.Error()) == "" || strings.HasPrefix(err.Error(), "exit status") {
			err = errors.New("git worktree add failed, perhaps in a hook")
		}
		if exists(p.Path) {
			return p, fmt.Errorf("%w. The worktree stays at %s", err, p.Path)
		}
		return p, err
	}
	return p, nil
}

// Finish readies a worktree Add made. Problems are reported, and don't stop a thread
// from starting there.
func Finish(ctx context.Context, p Plan) []string {
	var log []string
	switch p.Action {
	case ActionAttach:
		return nil
	case ActionTrackRemote:
		log = append(log, setTracking(ctx, p)...)
	case ActionDetached:
		// What these name would come from the code under review.
		if exists(filepath.Join(p.Path, ".gitmodules")) || exists(filepath.Join(p.Path, ".worktreeinclude")) {
			log = append(log, "Didn't initialize submodules or copy .worktreeinclude files: this pull request is code under review.")
		}
		return log
	}
	if p.FastForward != nil {
		log = append(log, fastForward(ctx, p)...)
	}
	return append(log, provision(ctx, p.Source, p.Path)...)
}

// setTracking makes a new branch track the remote one it came from. A single-branch
// clone doesn't fetch that one, so it is added to what the remote fetches.
func setTracking(ctx context.Context, p Plan) []string {
	for _, setting := range [][2]string{{"remote", p.Fetch.Remote}, {"merge", p.Fetch.From}} {
		if _, err := git(ctx, readTimeout, p.Source, "config", "branch."+p.Branch+"."+setting[0], setting[1]); err != nil {
			return []string{fmt.Sprintf("Couldn't make %s track %s: %v", p.Branch, p.StartLabel, err)}
		}
	}
	if _, ok := attempt(ctx, p.Source, "rev-parse", "--verify", "--quiet", "--end-of-options", "refs/heads/"+p.Branch+"@{upstream}"); !ok {
		if _, err := git(ctx, readTimeout, p.Source, "remote", "set-branches", "--add", p.Fetch.Remote, p.Branch); err != nil {
			return []string{fmt.Sprintf("Couldn't make %s track %s: %v", p.Branch, p.StartLabel, err)}
		}
	}
	return nil
}

// fastForward moves the branch this fresh worktree has checked out, so git refuses
// it elsewhere meanwhile. Anything but a pure fast-forward leaves it as it is.
func fastForward(ctx context.Context, p Plan) []string {
	upstream := strings.TrimPrefix(p.FastForward.Into, "refs/remotes/")
	if err := fetch(ctx, p.Source, *p.FastForward); err != nil {
		return []string{fmt.Sprintf("Couldn't fetch %s, so %s stays as it was.", upstream, p.Branch)}
	}
	target, err := git(ctx, readTimeout, p.Path, "rev-parse", "--verify", "--end-of-options", p.FastForward.Into+"^{commit}")
	if err != nil {
		return nil
	}
	if _, ok := attempt(ctx, p.Path, "merge-base", "--is-ancestor", "HEAD", target); !ok {
		return []string{fmt.Sprintf("%s has diverged from %s, so it stays as it was.", p.Branch, upstream)}
	}
	if head, _ := attempt(ctx, p.Path, "rev-parse", "HEAD"); head == target {
		return nil
	}
	if _, err := git(ctx, checkoutTimeout, p.Path, "merge", "--ff-only", "--quiet", target); err != nil {
		return []string{fmt.Sprintf("Couldn't fast-forward %s to %s: %v", p.Branch, upstream, err)}
	}
	return []string{fmt.Sprintf("Fast-forwarded %s to %s.", p.Branch, upstream)}
}

// provision readies a new worktree. Problems are reported, and never stop the
// thread from starting.
func provision(ctx context.Context, source, path string) []string {
	var log []string
	if exists(filepath.Join(path, ".gitmodules")) {
		if _, err := git(ctx, submoduleTimeout, path, "submodule", "update", "--init", "--recursive"); err != nil {
			log = append(log, fmt.Sprintf("Submodules weren't initialized: %v", err))
		}
	}
	return append(log, copyIncludes(ctx, source, path)...)
}

// copyIncludes copies untracked files matching the worktree's tracked
// .worktreeinclude, such as .env, from the source checkout. It copies regular files
// only, follows no symlinks, and never replaces a file.
func copyIncludes(ctx context.Context, source, path string) []string {
	patterns := filepath.Join(path, ".worktreeinclude")
	if info, err := os.Lstat(patterns); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	out, err := git(ctx, readTimeout, source, "ls-files", "-z", "--others", "--ignored", "--exclude-from="+patterns)
	if err != nil {
		return []string{"Couldn't read .worktreeinclude, so no files were copied."}
	}
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return []string{"Couldn't read the new worktree, so no files were copied from .worktreeinclude."}
	}
	copied, skipped := 0, 0
	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" {
			continue
		}
		if copyInclude(filepath.Join(source, rel), root, rel) {
			copied++
		} else {
			skipped++
		}
	}
	if copied == 0 && skipped == 0 {
		return nil
	}
	message := fmt.Sprintf("Copied %d files from .worktreeinclude.", copied)
	if copied == 1 {
		message = "Copied 1 file from .worktreeinclude."
	}
	if skipped > 0 {
		message += fmt.Sprintf(" Skipped %d that already exist, are symlinks or can't be read.", skipped)
	}
	return []string{message}
}

func copyInclude(from, root, rel string) bool {
	info, err := os.Lstat(from)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	to := filepath.Join(root, rel)
	if exists(to) {
		return false
	}
	// A tracked symlink to a folder must not carry the copy out of the worktree.
	parent, err := canonical(filepath.Dir(to))
	if err != nil || !Contains(root, parent) {
		return false
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false
	}
	in, err := os.Open(from)
	if err != nil {
		return false
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(parent, filepath.Base(to)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return false
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err != nil || closeErr != nil {
		_ = os.Remove(out.Name())
		return false
	}
	return true
}

// RemovalCheck decides whether removing a worktree could lose work. The first Run may
// ask submodules' remotes about their tags, and so wait on a network. Later Runs check
// again but reuse those answers, so they can run where waiting wouldn't be acceptable.
type RemovalCheck struct {
	ran  bool
	tags map[string]map[string]bool // what each repository's remote said it has
}

// Run reports why removing a worktree could lose work, before anything is disturbed.
// An error here means nothing has changed.
func (c *RemovalCheck) Run(ctx context.Context, source, path string) error {
	err := c.run(ctx, source, path)
	c.ran = true
	return err
}

func (c *RemovalCheck) run(ctx context.Context, source, path string) error {
	worktrees, err := list(ctx, source)
	if err != nil {
		return err
	}
	for _, w := range worktrees {
		if w.Path != path {
			continue
		}
		switch {
		case w.Main:
			return errors.New("this is the repository's primary checkout, which can't be deleted")
		case w.Locked:
			return errors.New("nothing was deleted: this worktree is locked; unlock it with git worktree unlock first")
		case w.Broken:
			return errors.New("nothing was deleted: git no longer recognizes this folder as a worktree. Save what you need from it, delete it, and run git worktree prune")
		case w.Missing:
			return nil
		}
		return c.checkClean(ctx, path, w)
	}
	return fmt.Errorf("%s isn't a worktree of this repository", path)
}

// checkFiles refuses if a checkout has anything a removal would lose: changes and
// untracked files, whatever the user's configuration hides, and files whose changes
// git has been told not to report.
func checkFiles(ctx context.Context, dir string) error {
	changes, err := git(ctx, readTimeout, dir, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	if changes != "" {
		return errors.New("it has uncommitted changes or untracked files; commit or remove them first")
	}
	tagged, err := git(ctx, readTimeout, dir, "ls-files", "-v")
	if err != nil {
		return err
	}
	sparse, _ := attempt(ctx, dir, "config", "--type=bool", "core.sparseCheckout")
	for _, line := range strings.Split(tagged, "\n") {
		// Lowercase tags are assume-unchanged. Skip-worktree is how a sparse checkout
		// leaves files out, and otherwise hides them just the same.
		if line != "" && (line[0] >= 'a' && line[0] <= 'z' || line[0] == 'S' && sparse != "true") {
			return errors.New("it has files marked assume-unchanged or skip-worktree, whose changes git doesn't report; clear those flags with git update-index first")
		}
	}
	return nil
}

func (c *RemovalCheck) checkClean(ctx context.Context, path string, w Worktree) error {
	if err := checkFiles(ctx, path); err != nil {
		return fmt.Errorf("nothing was deleted: %w", err)
	}
	if w.Branch == "" {
		kept, err := detachedWorkKept(ctx, path)
		if err != nil {
			return err
		}
		if !kept {
			return errors.New("nothing was deleted: it has commits on no branch or tag; put them on a branch first")
		}
	}
	return c.checkSubmodules(ctx, path)
}

// detachedWorkKept reports whether a detached HEAD has nothing that would be lost with
// its worktree and reflog: a ref that outlives the worktree has its commit, or HEAD is
// where the worktree began, as when reviewing a fork's pull request. Its reflog then
// holds only the record of its creation. The number of entries alone wouldn't show that,
// as Git expires old ones. Without such a record, as in a repository with no reflogs,
// there's no telling, so it counts as work.
func detachedWorkKept(ctx context.Context, path string) (bool, error) {
	refs, err := git(ctx, readTimeout, path, "for-each-ref", "--count=1", "--contains", "HEAD", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return false, err
	}
	if refs != "" {
		return true, nil
	}
	logs, err := git(ctx, readTimeout, path, "rev-parse", "--git-path", "logs/HEAD")
	if err != nil {
		return false, err
	}
	if !filepath.IsAbs(logs) {
		logs = filepath.Join(path, logs)
	}
	entries, err := os.ReadFile(logs)
	if err != nil {
		return false, nil
	}
	// Each entry starts with the commit HEAD moved from: none, for the first.
	entry, rest, _ := strings.Cut(strings.TrimRight(string(entries), "\n"), "\n")
	old, _, _ := strings.Cut(entry, " ")
	return rest == "" && old != "" && strings.Trim(old, "0") == "", nil
}

// checkSubmodules refuses if a submodule repository holds work only it has: all of them
// go with the worktree, whether checked out or kept after a deinit. Work means changes,
// commits on local branches or stashed that no remote has, tags their remote doesn't
// have on commits no remote has, or a HEAD no remote has and no published commit of the
// repository above records. Published commits are taken to be on their way to the
// submodule's remote, as a commit can be recorded from anywhere it was once fetched
// from, such as a pull request's head.
func (c *RemovalCheck) checkSubmodules(ctx context.Context, path string) error {
	checkedOut := map[string]bool{}
	if err := c.checkCheckouts(ctx, path, "", checkedOut); err != nil {
		return err
	}
	modules, err := git(ctx, readTimeout, path, "rev-parse", "--git-path", "modules")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(modules) {
		modules = filepath.Join(path, modules)
	}
	return c.checkKept(ctx, modules, "", checkedOut)
}

func submoduleWork(name string) error {
	return fmt.Errorf("nothing was deleted: submodule %s has commits that aren't on its remote; push them first or, if they aren't yours, use git worktree remove --force", name)
}

// checkCheckouts checks every submodule checked out under top, in either layout: its
// repository can be kept with the worktree's, or inside the submodule's folder. A
// gitlink with nothing checked out, with or without an entry in .gitmodules, has no
// repository here.
func (c *RemovalCheck) checkCheckouts(ctx context.Context, top, name string, checkedOut map[string]bool) error {
	out, err := git(ctx, readTimeout, top, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(out, "\x00") {
		header, sub, ok := strings.Cut(entry, "\t")
		if !ok || !strings.HasPrefix(header, "160000 ") {
			continue
		}
		dir := filepath.Join(top, sub)
		if !exists(filepath.Join(dir, ".git")) {
			continue
		}
		display := filepath.Join(name, sub)
		repository, err := git(ctx, readTimeout, dir, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return err
		}
		checkedOut[canonicalOr(repository)] = true
		if err := checkFiles(ctx, dir); err != nil {
			return fmt.Errorf("nothing was deleted: submodule %s: %w", display, err)
		}
		if err := c.checkRefs(ctx, dir, display); err != nil {
			return err
		}
		published, err := headPublished(ctx, top, sub, dir)
		if err != nil {
			return err
		}
		if !published {
			return submoduleWork(display)
		}
		if err := c.checkCheckouts(ctx, dir, display, checkedOut); err != nil {
			return err
		}
	}
	return nil
}

// checkKept checks the repositories in a modules folder, where one can stay with nothing
// checked out from it. Module names can contain slashes, so a folder may hold further
// names rather than be a repository.
func (c *RemovalCheck) checkKept(ctx context.Context, dir, name string, checkedOut map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path, display := filepath.Join(dir, entry.Name()), filepath.Join(name, entry.Name())
		if !exists(filepath.Join(path, "HEAD")) || !exists(filepath.Join(path, "objects")) {
			if err := c.checkKept(ctx, path, display, checkedOut); err != nil {
				return err
			}
			continue
		}
		if err := c.checkRefs(ctx, path, display); err != nil {
			return err
		}
		if !checkedOut[canonicalOr(path)] {
			local, err := git(ctx, readTimeout, path, "rev-list", "--max-count=1", "HEAD", "--not", "--remotes")
			if err != nil {
				return err
			}
			if local != "" {
				return submoduleWork(display)
			}
		}
		if err := c.checkKept(ctx, filepath.Join(path, "modules"), display, checkedOut); err != nil {
			return err
		}
	}
	return nil
}

func canonicalOr(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// checkRefs refuses if a submodule repository's branches, stash or tags hold commits
// no remote has, or if other worktrees depend on it. Only its remote can tell a tag
// fetched from it, which may well be on no branch, from a local one, so that takes
// asking.
func (c *RemovalCheck) checkRefs(ctx context.Context, dir, name string) error {
	worktrees, err := git(ctx, readTimeout, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	if strings.Count("\n"+worktrees, "\nworktree ") > 1 {
		return fmt.Errorf("nothing was deleted: submodule %s has worktrees of its own, which depend on its repository; remove them first", name)
	}
	local, err := git(ctx, readTimeout, dir, "rev-list", "--max-count=1", "--branches", "--glob=refs/stash", "--not", "--remotes")
	if err != nil {
		return err
	}
	if local != "" {
		return submoduleWork(name)
	}
	if off, err := git(ctx, readTimeout, dir, "rev-list", "--max-count=1", "--tags", "--not", "--remotes"); err != nil {
		return err
	} else if off == "" {
		return nil
	}
	published, err := c.remoteTags(ctx, dir)
	if err != nil {
		return fmt.Errorf("nothing was deleted: submodule %s has tags on commits no branch of its remote has, and its remote couldn't be asked about them", name)
	}
	tags, err := git(ctx, readTimeout, dir, "for-each-ref", "--format=%(objectname)%09%(refname)", "refs/tags")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(tags, "\n") {
		_, ref, ok := strings.Cut(line, "\t")
		if !ok || published[line] {
			continue
		}
		off, err := git(ctx, readTimeout, dir, "rev-list", "--max-count=1", ref, "--not", "--remotes")
		if err != nil {
			return err
		}
		if off != "" {
			return submoduleWork(name)
		}
	}
	return nil
}

// remoteTags is what a repository's remote said about its tags, asking only in the
// first Run. A later Run finding a repository not asked about says it can't tell.
func (c *RemovalCheck) remoteTags(ctx context.Context, dir string) (map[string]bool, error) {
	key := canonicalOr(dir)
	if published, ok := c.tags[key]; ok {
		return published, nil
	}
	if c.ran {
		return nil, errors.New("not asked")
	}
	out, err := git(ctx, remoteTimeout, dir, "ls-remote", "--tags", "--refs", "origin")
	if err != nil {
		return nil, err
	}
	published := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		published[line] = true
	}
	if c.tags == nil {
		c.tags = map[string]map[string]bool{}
	}
	c.tags[key] = published
	return published, nil
}

// headPublished reports whether a submodule's HEAD is on its remote, or is what a
// published commit of the repository above records for it.
func headPublished(ctx context.Context, top, sub, dir string) (bool, error) {
	local, err := git(ctx, readTimeout, dir, "rev-list", "--max-count=1", "HEAD", "--not", "--remotes")
	if err != nil {
		return false, err
	}
	if local == "" {
		return true, nil
	}
	head, err := git(ctx, readTimeout, dir, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	recorded, err := git(ctx, readTimeout, top, "log", "--remotes", "--format=", "--raw", "--no-abbrev", "--", sub)
	if err != nil {
		return false, err
	}
	return strings.Contains(recorded, " "+head+" "), nil
}

// Remove deletes a worktree with git worktree remove, which refuses one with
// uncommitted or untracked files or a lock. Git refuses any worktree of a repository
// with submodules unless forced, so only there, it is checked first and forced: git
// still refuses a lock, and Run takes the place of its own check. Killed
// partway, git leaves a folder it no longer recognizes, so it is left to finish.
func (c *RemovalCheck) Remove(ctx context.Context, source, path string) error {
	settled, done := settling(ctx)
	defer done()
	_, err := git(settled, checkoutTimeout, source, "worktree", "remove", "--", path)
	var failed *gitError
	if err == nil || !errors.As(err, &failed) || !strings.Contains(failed.stderr, "submodules") {
		return err
	}
	if err := c.Run(settled, source, path); err != nil {
		return err
	}
	_, err = git(settled, checkoutTimeout, source, "worktree", "remove", "--force", "--", path)
	return err
}

// DeleteBranch deletes a branch if it is fully merged, with git branch -d.
func DeleteBranch(ctx context.Context, source, branch string) error {
	if strings.HasPrefix(branch, "-") {
		return errors.New("invalid branch")
	}
	_, err := git(ctx, readTimeout, source, "branch", "-d", "--", branch)
	return err
}
