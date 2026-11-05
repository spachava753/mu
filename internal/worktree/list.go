package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spachava753/mu/internal/workspace"
)

// Worktree is one checkout of a repository, as git lists it.
type Worktree struct {
	Path    string // git's canonical path
	Branch  string // empty when detached
	Commit  string // short commit
	Main    bool   // the primary checkout, which cannot be removed
	Locked  bool
	Missing bool // git still lists it, but its folder is gone
	Broken  bool // its folder is there, but git no longer recognizes it as a worktree
	// Interrupted means git never finished checking it out.
	Interrupted bool
}

// Name says what the worktree has checked out.
func (w Worktree) Name() string {
	switch {
	case w.Branch != "":
		return w.Branch
	case w.Commit != "":
		return "detached at " + w.Commit
	}
	return filepath.Base(w.Path)
}

// Branch is a branch to work on. Local branches absorb their upstream.
type Branch struct {
	Name          string
	Remote        string // the remote it would be fetched from; empty for a local branch
	Upstream      string // of a local branch, such as origin/main
	Ahead, Behind int
	Worktree      string // the checkout that has it, if any
	Unfetched     bool   // the remote has it, but no local ref does yet
	Updated       int64  // Unix seconds of its last commit; 0 when unfetched
}

// Listing describes the repository a folder is in. It is empty outside one.
type Listing struct {
	Root        string // the top of the folder's checkout; empty outside a repository
	Prefix      string // the folder's path inside the checkout, if it is a subfolder
	Worktrees   []Worktree
	Branches    []Branch // most recent first
	Remotes     []string
	DefaultBase string // what a new branch starts from, such as origin/main
}

// Repo reports whether the folder is in a repository.
func (l Listing) Repo() bool { return l.Root != "" }

// Read lists the repository from local refs only, without any network access. A
// folder outside a repository, a missing folder or a machine without git reads as
// empty. An error means the answer is unknown.
func Read(ctx context.Context, dir string) (Listing, error) {
	if g, err := workspace.InspectGit(ctx, dir); err != nil || !g.Repo() {
		return Listing{}, err
	}
	out, err := git(ctx, readTimeout, dir, "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return Listing{}, err
	}
	root, prefix, _ := strings.Cut(out, "\n")
	l := Listing{Root: root, Prefix: filepath.Clean(prefix)}
	if prefix == "" {
		l.Prefix = ""
	}
	if l.Worktrees, err = list(ctx, root); err != nil {
		return Listing{}, err
	}
	if l.Remotes, err = remotes(ctx, root); err != nil {
		return Listing{}, err
	}
	if l.Branches, err = branches(ctx, root, l.Remotes); err != nil {
		return Listing{}, err
	}
	base, err := defaultBase(ctx, root, DefaultRemote(l.Remotes), "", false)
	if err != nil {
		return Listing{}, err
	}
	l.DefaultBase = base.label
	return l, nil
}

// list parses git worktree list. Entries are separated by blank lines, and a bare
// repository's own entry has no checkout to offer.
func list(ctx context.Context, dir string) ([]Worktree, error) {
	out, err := git(ctx, readTimeout, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var result []Worktree
	for i, record := range strings.Split(out, "\n\n") {
		var w Worktree
		bare := false
		for _, line := range strings.Split(record, "\n") {
			key, value, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				w.Path = value
			case "HEAD":
				w.Commit = value[:min(len(value), 7)]
			case "branch":
				w.Branch = strings.TrimPrefix(value, "refs/heads/")
			case "bare":
				bare = true
			case "locked":
				w.Locked = true
				w.Interrupted = value == "initializing"
			case "prunable":
				w.Broken = true
			}
		}
		w.Main = i == 0
		w.Missing, w.Broken = w.Broken && !exists(w.Path), w.Broken && exists(w.Path)
		if w.Path != "" && !bare {
			result = append(result, w)
		}
	}
	return result, nil
}

func remotes(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, readTimeout, dir, "remote")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// DefaultRemote is origin, or else the first remote, or else none.
func DefaultRemote(remotes []string) string {
	switch {
	case len(remotes) == 0:
		return ""
	case slices.Contains(remotes, "origin"):
		return "origin"
	}
	return remotes[0]
}

// splitRemoteRef splits refs/remotes/<remote>/<branch>, or <remote>/<branch>,
// allowing remote names that contain slashes.
func splitRemoteRef(ref string, remotes []string) (remote, branch string, ok bool) {
	rest := strings.TrimPrefix(ref, "refs/remotes/")
	for _, r := range remotes {
		if b, found := strings.CutPrefix(rest, r+"/"); found && len(r) > len(remote) {
			remote, branch, ok = r, b, true
		}
	}
	return remote, branch, ok
}

type refRow struct {
	ref, date, worktree, upstream, upstreamRemote, upstreamRef, track, symref string
}

func refRows(ctx context.Context, dir string, patterns ...string) ([]refRow, error) {
	args := append([]string{"for-each-ref", "--sort=-committerdate", "--format=%(refname)%00%(committerdate:unix)%00%(worktreepath)%00%(upstream)%00%(upstream:remotename)%00%(upstream:remoteref)%00%(upstream:track)%00%(symref)", "--"}, patterns...)
	out, err := git(ctx, readTimeout, dir, args...)
	if err != nil {
		return nil, err
	}
	var rows []refRow
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) == 8 {
			rows = append(rows, refRow{f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7]})
		}
	}
	return rows, nil
}

// trackCounts reads %(upstream:track), such as "[ahead 1, behind 2]" or "[gone]".
func trackCounts(track string) (ahead, behind int, gone bool) {
	for _, part := range strings.Split(strings.Trim(track, "[]"), ", ") {
		name, count, _ := strings.Cut(part, " ")
		n, _ := strconv.Atoi(count)
		switch name {
		case "ahead":
			ahead = n
		case "behind":
			behind = n
		case "gone":
			gone = true
		}
	}
	return ahead, behind, gone
}

func branches(ctx context.Context, dir string, remotes []string) ([]Branch, error) {
	rows, err := refRows(ctx, dir, "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	upstreams := map[string]bool{}
	for _, r := range rows {
		if strings.HasPrefix(r.ref, "refs/heads/") && r.upstream != "" {
			upstreams[r.upstream] = true
		}
	}
	var result []Branch
	for _, r := range rows {
		updated, _ := strconv.ParseInt(r.date, 10, 64)
		if name, local := strings.CutPrefix(r.ref, "refs/heads/"); local {
			ahead, behind, _ := trackCounts(r.track)
			result = append(result, Branch{Name: name, Upstream: strings.TrimPrefix(r.upstream, "refs/remotes/"), Ahead: ahead, Behind: behind, Worktree: r.worktree, Updated: updated})
			continue
		}
		if r.symref != "" || upstreams[r.ref] {
			continue // origin/HEAD, or already shown as its local branch.
		}
		if remote, name, ok := splitRemoteRef(r.ref, remotes); ok {
			result = append(result, Branch{Name: name, Remote: remote, Updated: updated})
		}
	}
	return result, nil
}

// PullRequest is an open pull request, as gh lists it.
type PullRequest struct {
	Number     int
	Title      string
	Head, Base string
	Author     string
	Draft      bool
	Fork       bool
}

// ghCommand prepares gh in dir, or returns nil without it.
func ghCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	endWithGroup(cmd)
	cmd.Env = append(workspace.FolderEnv(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "LC_ALL=C")
	return cmd
}

// Remote is what only the remotes can say.
type Remote struct {
	Unfetched    []Branch // branches no local ref knows of
	PullRequests []PullRequest
	Head         string // the default remote's default branch, as it says now
}

// ReadRemote asks the remotes for what local refs can't say, and with gh, for open
// pull requests. It contacts them with the user's own credentials. What can't be
// reached is left out.
func ReadRemote(ctx context.Context, l Listing) (r Remote) {
	known := map[[2]string]bool{}
	for _, b := range l.Branches {
		if b.Remote != "" {
			known[[2]string{b.Remote, b.Name}] = true
		} else if remote, name, ok := splitRemoteRef(b.Upstream, l.Remotes); ok {
			known[[2]string{remote, name}] = true
		}
	}
	for _, remote := range l.Remotes {
		out, err := git(ctx, remoteTimeout, l.Root, "ls-remote", "--heads", "--", remote)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			_, ref, _ := strings.Cut(line, "\t")
			if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok && !known[[2]string{remote, name}] {
				r.Unfetched = append(r.Unfetched, Branch{Name: name, Remote: remote, Unfetched: true})
			}
		}
	}
	r.Head = RemoteHead(ctx, l)
	listCtx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	cmd := ghCommand(listCtx, l.Root, "pr", "list", "--limit", "50", "--json", "number,title,headRefName,baseRefName,author,isDraft,isCrossRepository")
	if cmd == nil {
		return r
	}
	out, err := cmd.Output()
	if err != nil {
		return r
	}
	var reply []struct {
		Number            int    `json:"number"`
		Title             string `json:"title"`
		HeadRefName       string `json:"headRefName"`
		BaseRefName       string `json:"baseRefName"`
		IsDraft           bool   `json:"isDraft"`
		IsCrossRepository bool   `json:"isCrossRepository"`
		Author            struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if json.Unmarshal(out, &reply) != nil {
		return r
	}
	for _, p := range reply {
		r.PullRequests = append(r.PullRequests, PullRequest{Number: p.Number, Title: p.Title, Head: p.HeadRefName, Base: p.BaseRefName, Author: p.Author.Login, Draft: p.IsDraft, Fork: p.IsCrossRepository})
	}
	return r
}

// RemoteHead asks a folder's default remote for its default branch, or says nothing
// if it can't.
func RemoteHead(ctx context.Context, l Listing) string {
	remote := DefaultRemote(l.Remotes)
	if remote == "" {
		return ""
	}
	return advertisedHead(ctx, l.Root, remote)
}

// advertisedHead asks a remote for its default branch.
func advertisedHead(ctx context.Context, dir, remote string) string {
	out, err := git(ctx, remoteTimeout, dir, "ls-remote", "--symref", "--", remote, "HEAD")
	if err != nil {
		return ""
	}
	if first, _, _ := strings.Cut(out, "\n"); strings.HasPrefix(first, "ref: refs/heads/") {
		branch, _, _ := strings.Cut(strings.TrimPrefix(first, "ref: refs/heads/"), "\t")
		return branch
	}
	return ""
}

// pullRequestDetails is what gh knows about one pull request.
type pullRequestDetails struct {
	URL  string `json:"url"`
	Head string `json:"headRefName"`
	Fork bool   `json:"isCrossRepository"`
}

// pullDetails asks gh about one pull request. Without gh, or signed out, known is
// false. An error means it doesn't exist.
func pullDetails(ctx context.Context, dir string, number int) (details pullRequestDetails, known bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	cmd := ghCommand(ctx, dir, "pr", "view", strconv.Itoa(number), "--json", "url,headRefName,isCrossRepository")
	if cmd == nil {
		return details, false, nil
	}
	out, runErr := cmd.Output()
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) && strings.Contains(strings.ToLower(string(exit.Stderr)), "could not resolve to a pull") {
			return details, false, fmt.Errorf("pull request #%d wasn't found", number)
		}
		return details, false, nil
	}
	if json.Unmarshal(out, &details) != nil {
		return details, false, nil
	}
	return details, true, nil
}

// Contains reports whether path is the worktree's folder or inside it.
func Contains(worktree, path string) bool {
	rel, err := filepath.Rel(worktree, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// exists reports whether anything is at path, even a dangling symlink.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}
