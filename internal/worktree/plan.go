package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Kind is what a thread is to work on.
type Kind string

const (
	KindNew         Kind = "new"    // a new branch, in a new worktree
	KindBranch      Kind = "branch" // an existing branch, local or on a remote
	KindPullRequest Kind = "pr"     // a pull request's head, in a detached worktree
	KindPath        Kind = "path"   // an existing worktree, exactly as it is
)

// Target is the user's intent. Resolve works out the Git steps.
type Target struct {
	Kind   Kind
	Branch string // KindNew and KindBranch
	Base   string // KindNew: what to start from; empty for the default branch
	Remote string // KindBranch: a remote-only branch's remote
	Number int    // KindPullRequest
	Path   string // KindPath
}

// Action is what Create does for a plan.
type Action string

const (
	ActionAttach      Action = "attach"       // use a checkout that exists; no Git changes
	ActionNewBranch   Action = "new-branch"   // a new worktree on a new branch
	ActionLocalBranch Action = "local-branch" // a new worktree on a local branch
	ActionTrackRemote Action = "track-remote" // a new worktree on a new branch tracking a remote one
	ActionDetached    Action = "detached"     // a new worktree with a detached HEAD
)

// Fetch brings a single ref from a remote: into a remote-tracking ref, or with Into
// empty, only its commits, for a plan that names the commit itself.
type Fetch struct {
	Remote, From, Into string
}

// Plan is one concrete way to carry out a target. Making one reads local state and
// may query the remote and gh, but writes no refs, config or files.
type Plan struct {
	Action      Action
	Source      string // the checkout to run git in
	Path        string // the worktree's folder
	Prefix      string // the subfolder threads work in, as the project's folder is in its checkout
	Branch      string
	Template    string // where worktrees go
	Folder      string // what {branch} stands for in the template
	Start       string // what the worktree starts at
	StartLabel  string // the same, for reading
	Fetch       *Fetch // fetched before creating
	FastForward *Fetch // after creating, the local branch fast-forwards to this if it can
	Warnings    []string
}

// SuggestBranch names a new branch when the user doesn't.
func SuggestBranch() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "mu/" + time.Now().Format("2006-01-02") + "-" + hex.EncodeToString(b[:])
}

var templateVariable = regexp.MustCompile(`\{[^{}]*\}`)

// ValidateTemplate checks a path template: a complete path with the variables
// {repo}, {repoPath}, {checkoutParent} and {branch}.
func ValidateTemplate(template string) error {
	for _, variable := range templateVariable.FindAllString(template, -1) {
		switch variable {
		case "{repo}", "{repoPath}", "{checkoutParent}", "{branch}":
		default:
			return fmt.Errorf("%s isn't a variable; use {repo}, {repoPath}, {checkoutParent} or {branch}", variable)
		}
	}
	rest := templateVariable.ReplaceAllString(template, "")
	if strings.ContainsAny(rest, "{}") {
		return errors.New("the template has an unmatched brace")
	}
	if strings.HasPrefix(template, "~") && template != "~" && !strings.HasPrefix(template, "~"+string(filepath.Separator)) {
		return errors.New("only a leading ~/ is expanded, not another user's home folder")
	}
	if base := filepath.Base(template); base == "." || base == ".." || base == string(filepath.Separator) || strings.HasSuffix(template, string(filepath.Separator)) {
		return errors.New("the template must end in a folder name")
	}
	return nil
}

var branchUnsafe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// repoName is the primary checkout's folder name, even from a linked worktree.
func repoName(ctx context.Context, dir string) (string, error) {
	common, err := git(ctx, readTimeout, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if filepath.Base(common) == ".git" {
		return filepath.Base(filepath.Dir(common)), nil
	}
	return strings.TrimSuffix(filepath.Base(common), ".git"), nil // A bare repository.
}

// canonical resolves symlinks in the part of path that exists, so that Mu's path
// for a new worktree is the one git will list.
func canonical(path string) (string, error) {
	rest := ""
	for current := path; ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		if current == filepath.Dir(current) {
			return path, nil
		}
		rest = filepath.Join(filepath.Base(current), rest)
	}
}

// destination expands the template for a worktree. A folder that already exists,
// or that git still has registered, gets -2, -3… appended.
func destination(ctx context.Context, template string, l Listing, folder string) (string, error) {
	if err := ValidateTemplate(template); err != nil {
		return "", err
	}
	name, err := repoName(ctx, l.Root)
	if err != nil {
		return "", err
	}
	path := strings.NewReplacer("{repo}", name, "{repoPath}", l.Root, "{checkoutParent}", filepath.Dir(l.Root), "{branch}", branchUnsafe.ReplaceAllString(folder, "-")).Replace(template)
	if path == "~" || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(l.Root, path)
	}
	path, err = canonical(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Stat(parent)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("worktrees can't go in %s: it isn't a folder", parent)
			}
			break
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	registered := func(p string) bool {
		return slices.ContainsFunc(l.Worktrees, func(w Worktree) bool { return w.Path == p })
	}
	candidate := path
	for n := 2; n < 100; n++ {
		if !exists(candidate) && !registered(candidate) {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", path, n)
	}
	return "", errors.New("too many folders with this name; change the worktree folder in Settings")
}

// start is where a new worktree begins.
type start struct {
	ref, label string
	fetch      *Fetch
	warning    string
}

func remoteStart(remote, branch string) start {
	into := "refs/remotes/" + remote + "/" + branch
	return start{ref: into, label: remote + "/" + branch, fetch: &Fetch{Remote: remote, From: "refs/heads/" + branch, Into: into}}
}

// defaultBase is the remote's default branch, or the local default branch. The
// remote's own word is head, if known. With network, a default branch that was never
// fetched is asked of the remote.
func defaultBase(ctx context.Context, dir, remote, head string, network bool) (start, error) {
	if remote != "" {
		if head != "" {
			return remoteStart(remote, head), nil
		}
		head, _ := attempt(ctx, dir, "symbolic-ref", "--quiet", "refs/remotes/"+remote+"/HEAD")
		for _, ref := range []string{head, "refs/remotes/" + remote + "/main", "refs/remotes/" + remote + "/master"} {
			if ref != "" && refExists(ctx, dir, ref) {
				return remoteStart(remote, strings.TrimPrefix(ref, "refs/remotes/"+remote+"/")), nil
			}
		}
		if network {
			if branch := advertisedHead(ctx, dir, remote); branch != "" {
				return remoteStart(remote, branch), nil
			}
			local, localErr := defaultBase(ctx, dir, "", "", false)
			local.warning = fmt.Sprintf("Couldn't find %s's default branch; starting from local %s.", remote, local.label)
			return local, localErr
		}
	}
	for _, branch := range []string{"main", "master"} {
		if refExists(ctx, dir, "refs/heads/"+branch) {
			return start{ref: "refs/heads/" + branch, label: branch}, nil
		}
	}
	if current, ok := attempt(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); ok {
		return start{ref: "refs/heads/" + current, label: current}, nil
	}
	return start{ref: "HEAD", label: "HEAD"}, nil
}

// resolveBase finds what a new branch starts from: a ref or commit known locally,
// or a branch or tag on a remote.
func resolveBase(ctx context.Context, l Listing, base string) (start, error) {
	dir := l.Root
	unknown := fmt.Errorf("unknown base %q; use a branch, a commit, or a tag you've fetched", base)
	if strings.HasPrefix(base, "-") {
		return start{}, unknown
	}
	if refExists(ctx, dir, base) {
		full, _ := attempt(ctx, dir, "rev-parse", "--symbolic-full-name", "--verify", "--end-of-options", base)
		if strings.HasPrefix(full, "refs/remotes/") {
			if remote, branch, ok := splitRemoteRef(full, l.Remotes); ok {
				return remoteStart(remote, branch), nil
			}
			return start{ref: full, label: strings.TrimPrefix(full, "refs/remotes/")}, nil
		}
		if strings.HasPrefix(full, "refs/") {
			return start{ref: full, label: strings.TrimPrefix(strings.TrimPrefix(full, "refs/heads/"), "refs/tags/")}, nil
		}
		// A commit expression such as HEAD~1 would mean something else in the new
		// worktree, so it is pinned now.
		commit, err := git(ctx, readTimeout, dir, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
		if err != nil {
			return start{}, unknown
		}
		return start{ref: commit, label: base}, nil
	}
	// Not known locally: maybe a branch on a remote.
	remote, branch, qualified := splitRemoteRef(base, l.Remotes)
	if !qualified {
		remote, branch = DefaultRemote(l.Remotes), base
	}
	if remote == "" || !validBranchName(ctx, dir, branch) {
		return start{}, unknown
	}
	out, err := git(ctx, remoteTimeout, dir, "ls-remote", "--heads", "--", remote, "refs/heads/"+branch)
	if err != nil {
		return start{}, fmt.Errorf("couldn't reach %s to find %q", remote, branch)
	}
	if out == "" {
		return start{}, unknown
	}
	return remoteStart(remote, branch), nil
}

// refConflict finds a branch that can't exist alongside a new one, since refs are
// files: feature/x can't exist with feature, or feature with feature/x.
func refConflict(ctx context.Context, dir, branch string) string {
	for parent := filepath.Dir(branch); parent != "."; parent = filepath.Dir(parent) {
		if refExists(ctx, dir, "refs/heads/"+parent) {
			return parent
		}
	}
	child, _ := attempt(ctx, dir, "for-each-ref", "--count=1", "--format=%(refname:short)", "refs/heads/"+branch+"/")
	return child
}

// Options is what a plan depends on besides the repository.
type Options struct {
	Template   string // where worktrees go
	RemoteHead string // the default remote's default branch, if known from ReadRemote
}

// Resolve works out the plan for a target, in the repository of a project's
// folder. It is used for both the preview and the creation, so they can't disagree.
func Resolve(ctx context.Context, dir string, t Target, o Options) (Plan, error) {
	l, err := Read(ctx, dir)
	if err != nil {
		return Plan{}, err
	}
	if !l.Repo() {
		return Plan{}, errors.New("this folder isn't a Git repository")
	}
	var p Plan
	switch t.Kind {
	case KindPath:
		p, err = planPath(l, t.Path)
	case KindNew:
		p, err = planNew(ctx, l, t, o)
	case KindBranch:
		p, err = planBranch(ctx, l, t.Branch, t.Remote, o.Template)
	case KindPullRequest:
		p, err = planPullRequest(ctx, l, t.Number, o.Template)
	default:
		err = errors.New("choose what to work on")
	}
	if err != nil {
		return Plan{}, err
	}
	p.Source, p.Prefix, p.Template = l.Root, l.Prefix, o.Template
	return p, nil
}

func planPath(l Listing, path string) (Plan, error) {
	i := slices.IndexFunc(l.Worktrees, func(w Worktree) bool { return w.Path == path })
	if i < 0 {
		return Plan{}, fmt.Errorf("%s isn't a worktree of this repository", path)
	}
	switch w := l.Worktrees[i]; {
	case w.Missing:
		return Plan{}, fmt.Errorf("the folder of this worktree is gone: %s", path)
	case w.Broken:
		return Plan{}, fmt.Errorf("git no longer recognizes this worktree: %s", path)
	case w.Interrupted:
		return Plan{}, fmt.Errorf("git never finished checking out this worktree; remove it with git worktree unlock and git worktree remove --force: %s", path)
	}
	return Plan{Action: ActionAttach, Path: path, Branch: l.Worktrees[i].Branch}, nil
}

func planNew(ctx context.Context, l Listing, t Target, o Options) (Plan, error) {
	// Git takes these, but they mean something else wherever a branch name is read.
	if !validBranchName(ctx, l.Root, t.Branch) || t.Branch == "@" || strings.HasPrefix(t.Branch, "refs/") {
		return Plan{}, fmt.Errorf("%q isn't a valid branch name", t.Branch)
	}
	if refExists(ctx, l.Root, "refs/heads/"+t.Branch) {
		return Plan{}, fmt.Errorf("branch %s already exists; pick it from Branches instead", t.Branch)
	}
	if conflict := refConflict(ctx, l.Root, t.Branch); conflict != "" {
		return Plan{}, fmt.Errorf("branch %s can't exist alongside %s; choose another name", t.Branch, conflict)
	}
	var base start
	var err error
	if t.Base != "" {
		base, err = resolveBase(ctx, l, t.Base)
	} else {
		base, err = defaultBase(ctx, l.Root, DefaultRemote(l.Remotes), o.RemoteHead, true)
	}
	if err != nil {
		return Plan{}, err
	}
	if base.fetch == nil && !refExists(ctx, l.Root, base.ref) {
		return Plan{}, errors.New("this repository has no commits yet; make one before starting a worktree")
	}
	path, err := destination(ctx, o.Template, l, t.Branch)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Action: ActionNewBranch, Path: path, Branch: t.Branch, Folder: t.Branch, Start: base.ref, StartLabel: base.label, Fetch: base.fetch}
	if base.warning != "" {
		p.Warnings = append(p.Warnings, base.warning)
	}
	if remote, name, ok := splitRemoteRef(t.Branch, l.Remotes); ok {
		p.Warnings = append(p.Warnings, fmt.Sprintf("This new branch's name reads like the branch %s on %s, which isn't known here.", name, remote))
	}
	return p, nil
}

func commits(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// planBranch attaches the checkout that has a branch, checks out a free local
// branch, or fetches and tracks a remote one.
func planBranch(ctx context.Context, l Listing, branch, remote, template string) (Plan, error) {
	if !validBranchName(ctx, l.Root, branch) {
		return Plan{}, fmt.Errorf("%q isn't a valid branch name", branch)
	}
	rows, err := refRows(ctx, l.Root, "refs/heads/"+branch)
	if err != nil {
		return Plan{}, err
	}
	// Patterns also match descendants: feature matches feature/x.
	i := slices.IndexFunc(rows, func(r refRow) bool { return r.ref == "refs/heads/"+branch })
	if i >= 0 {
		local := rows[i]
		if remote != "" && local.upstream != "refs/remotes/"+remote+"/"+branch {
			return Plan{}, fmt.Errorf("the local branch %s doesn't track %s/%s; pick the local branch, or rename it first", branch, remote, branch)
		}
		if local.worktree != "" {
			return planPath(l, local.worktree)
		}
		path, err := destination(ctx, template, l, branch)
		if err != nil {
			return Plan{}, err
		}
		p := Plan{Action: ActionLocalBranch, Path: path, Branch: branch, Folder: branch, Start: "refs/heads/" + branch, StartLabel: branch}
		ahead, behind, gone := trackCounts(local.track)
		if !strings.HasPrefix(local.upstream, "refs/remotes/") || local.upstreamRemote == "" || !strings.HasPrefix(local.upstreamRef, "refs/heads/") || gone {
			return p, nil
		}
		label := strings.TrimPrefix(local.upstream, "refs/remotes/")
		switch {
		case ahead > 0 && behind > 0:
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s has %s not on %s, and is behind it. It stays as it is.", branch, commits(ahead), label))
		case ahead > 0:
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s has %s not on %s yet.", branch, commits(ahead), label))
		default:
			p.FastForward = &Fetch{Remote: local.upstreamRemote, From: local.upstreamRef, Into: local.upstream}
			p.StartLabel = label
		}
		return p, nil
	}
	if remote == "" {
		remote = DefaultRemote(l.Remotes)
		// A branch on another remote only, listed once, is still unambiguous.
		if j := slices.IndexFunc(l.Branches, func(b Branch) bool { return b.Name == branch && b.Remote != "" }); j >= 0 && !refExists(ctx, l.Root, "refs/remotes/"+remote+"/"+branch) {
			remote = l.Branches[j].Remote
		}
	}
	if remote == "" {
		return Plan{}, fmt.Errorf("there's no branch %s, and this repository has no remote", branch)
	}
	if !slices.Contains(l.Remotes, remote) {
		return Plan{}, fmt.Errorf("there's no remote named %s", remote)
	}
	if conflict := refConflict(ctx, l.Root, branch); conflict != "" {
		return Plan{}, fmt.Errorf("branch %s can't exist alongside %s; rename the local one first", branch, conflict)
	}
	s := remoteStart(remote, branch)
	var warnings []string
	if !refExists(ctx, l.Root, s.ref) {
		out, err := git(ctx, remoteTimeout, l.Root, "ls-remote", "--heads", "--", remote, "refs/heads/"+branch)
		switch {
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("Couldn't reach %s to check %s; it is fetched when the thread starts.", remote, branch))
		case out == "":
			return Plan{}, fmt.Errorf("branch %s doesn't exist locally or on %s", branch, remote)
		}
	}
	path, err := destination(ctx, template, l, branch)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Action: ActionTrackRemote, Path: path, Branch: branch, Folder: branch, Start: s.ref, StartLabel: s.label, Fetch: s.fetch, Warnings: warnings}, nil
}

// remoteAddress splits a Git or web address into its host and owner/repo.
func remoteAddress(url string) (host, repo string) {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	url = strings.TrimSuffix(url, ".git")
	if _, rest, ok := strings.Cut(url, "://"); ok {
		url = rest
	} else {
		url = strings.Replace(url, ":", "/", 1) // scp-style, as in git@host:owner/repo
	}
	if _, rest, ok := strings.Cut(url, "@"); ok {
		url = rest
	}
	parts := strings.Split(url, "/")
	if len(parts) < 3 {
		return "", ""
	}
	host, _, _ = strings.Cut(parts[0], ":")
	return strings.ToLower(host), strings.ToLower(parts[len(parts)-2] + "/" + parts[len(parts)-1])
}

// remoteHosting finds the remote with a repository. One on the same host comes first.
// Another host's copy can be the same one, through an SSH alias or an alternate name,
// so it will do, and the preview names the remote.
func remoteHosting(ctx context.Context, l Listing, host, repo string) string {
	found := ""
	for _, remote := range l.Remotes {
		address, ok := attempt(ctx, l.Root, "remote", "get-url", "--", remote)
		if !ok {
			continue
		}
		h, r := remoteAddress(address)
		switch {
		case r != repo:
		case h == host:
			return remote
		case found == "":
			found = remote
		}
	}
	return found
}

// planPullRequest works on the branch underneath a pull request, like any other branch.
// A fork's branch isn't on a configured remote, a merged one's may be gone, and without
// gh there's no telling which it is, so those detach a new worktree at the head commit.
func planPullRequest(ctx context.Context, l Listing, number int, template string) (Plan, error) {
	if number <= 0 {
		return Plan{}, errors.New("enter a pull request number")
	}
	details, known, err := pullDetails(ctx, l.Root, number)
	if err != nil {
		return Plan{}, err
	}
	remote := DefaultRemote(l.Remotes)
	var warnings []string
	if known {
		// Fetch from the remote that hosts it, such as upstream in a fork workflow.
		web, _, _ := strings.Cut(details.URL, "/pull/")
		host, repo := remoteAddress(web)
		remote = remoteHosting(ctx, l, host, repo)
		if remote == "" {
			return Plan{}, fmt.Errorf("pull request #%d is in %s, which isn't a configured remote", number, repo)
		}
		if !details.Fork && details.Head != "" {
			if refExists(ctx, l.Root, "refs/heads/"+details.Head) {
				return planBranch(ctx, l, details.Head, "", template)
			}
			if out, err := git(ctx, remoteTimeout, l.Root, "ls-remote", "--heads", "--", remote, "refs/heads/"+details.Head); err != nil || out != "" {
				return planBranch(ctx, l, details.Head, remote, template)
			}
		}
	} else {
		warnings = append(warnings, "Without gh signed in, Mu can't find this pull request's branch, so it uses the head commit.")
	}
	if remote == "" {
		return Plan{}, errors.New("this repository has no remote to fetch the pull request from")
	}
	from := fmt.Sprintf("refs/pull/%d/head", number)
	out, err := git(ctx, remoteTimeout, l.Root, "ls-remote", "--", remote, from)
	if err != nil {
		return Plan{}, fmt.Errorf("couldn't reach %s to find pull request #%d", remote, number)
	}
	commit, _, ok := strings.Cut(out, "\t")
	if !ok {
		return Plan{}, fmt.Errorf("pull request #%d wasn't found on %s", number, remote)
	}
	folder := fmt.Sprintf("pr-%d", number)
	path, err := destination(ctx, template, l, folder)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Action: ActionDetached, Path: path, Folder: folder, Start: commit, StartLabel: fmt.Sprintf("PR #%d", number), Fetch: &Fetch{Remote: remote, From: from}, Warnings: warnings}, nil
}
