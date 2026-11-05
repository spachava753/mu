package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/mu/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.WithoutGH(m.Run)) }

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.file.allow", "GIT_CONFIG_VALUE_0=always")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func commit(t *testing.T, dir, file string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "--", file)
	run(t, dir, "commit", "-q", "-m", file)
	return run(t, dir, "rev-parse", "HEAD")
}

// clone makes a repository with an origin and returns the clone, the origin, and a
// second clone to push to the origin with, as a colleague would.
func clone(t *testing.T) (repo, origin, other string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := tempDir(t)
	origin = filepath.Join(base, "origin.git")
	run(t, base, "init", "-q", "--bare", "-b", "main", origin)
	other = filepath.Join(base, "other")
	run(t, base, "clone", "-q", origin, other)
	run(t, other, "checkout", "-q", "-b", "main")
	commit(t, other, "one")
	run(t, other, "push", "-q", "origin", "main")
	repo = filepath.Join(base, "app")
	run(t, base, "clone", "-q", origin, repo)
	return repo, origin, other
}

func resolve(t *testing.T, dir string, target Target) Plan {
	t.Helper()
	p, err := Resolve(context.Background(), dir, target, Options{Template: filepath.Join(filepath.Dir(dir), "wt", "{repo}", "{branch}")})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Result is what a plan's three steps come to.
type Result struct {
	Path, Cwd string
	Log       []string
}

func CheckRemovable(ctx context.Context, source, path string) error {
	return (&RemovalCheck{}).Run(ctx, source, path)
}

func Remove(ctx context.Context, source, path string) error {
	return (&RemovalCheck{}).Remove(ctx, source, path)
}

func Create(ctx context.Context, p Plan) (Result, error) {
	log, err := Prepare(ctx, p)
	if err != nil {
		return Result{}, err
	}
	if p, err = Add(ctx, p); err != nil {
		return Result{}, err
	}
	return Result{Path: p.Path, Cwd: p.WorkDir(), Log: append(log, Finish(ctx, p)...)}, nil
}

func create(t *testing.T, dir string, target Target) Result {
	t.Helper()
	r, err := Create(context.Background(), resolve(t, dir, target))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOutsideARepositoryReadsAsEmpty(t *testing.T) {
	l, err := Read(context.Background(), t.TempDir())
	if err != nil || l.Repo() {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := Resolve(context.Background(), t.TempDir(), Target{Kind: KindNew, Branch: "x"}, Options{Template: "/tmp/{branch}"}); err == nil {
		t.Fatal("planned a worktree outside a repository")
	}
}

func TestRead(t *testing.T) {
	repo, _, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "colleague")
	commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "colleague")
	run(t, repo, "fetch", "-q")
	result := create(t, repo, Target{Kind: KindNew, Branch: "feature/x"})
	l, err := Read(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if l.Root != repo || l.Prefix != "" || l.DefaultBase != "origin/main" || !slices.Equal(l.Remotes, []string{"origin"}) {
		t.Fatalf("%+v", l)
	}
	if len(l.Worktrees) != 2 || !l.Worktrees[0].Main || l.Worktrees[0].Branch != "main" || l.Worktrees[1].Path != result.Path || l.Worktrees[1].Branch != "feature/x" || l.Worktrees[1].Main {
		t.Fatalf("%+v", l.Worktrees)
	}
	find := func(name, remote string) *Branch {
		for i, b := range l.Branches {
			if b.Name == name && b.Remote == remote {
				return &l.Branches[i]
			}
		}
		return nil
	}
	// main is local, and absorbs origin/main.
	if b := find("main", ""); b == nil || b.Upstream != "origin/main" || b.Worktree != repo {
		t.Fatalf("%+v", l.Branches)
	}
	if find("main", "origin") != nil {
		t.Fatalf("upstream listed apart from its local branch: %+v", l.Branches)
	}
	if find("colleague", "origin") == nil || find("feature/x", "") == nil {
		t.Fatalf("%+v", l.Branches)
	}
}

func TestReadRemoteFindsWhatIsNotFetched(t *testing.T) {
	repo, _, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "late")
	commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "late")
	l, err := Read(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	testutil.FakeGH(t, `echo '[{"number":7,"title":"Add x","headRefName":"late","baseRefName":"main","isDraft":true,"isCrossRepository":false,"author":{"login":"sam"}}]'`)
	remote := ReadRemote(context.Background(), l)
	unfetched, pulls := remote.Unfetched, remote.PullRequests
	if len(unfetched) != 1 || unfetched[0].Name != "late" || unfetched[0].Remote != "origin" || !unfetched[0].Unfetched {
		t.Fatalf("%+v", unfetched)
	}
	if len(pulls) != 1 || pulls[0].Number != 7 || !pulls[0].Draft || pulls[0].Author != "sam" {
		t.Fatalf("%+v", pulls)
	}
	if remote.Head != "main" {
		t.Fatal(remote.Head)
	}
	// It tracks and fetches the branch, even though no local ref knows it.
	p := resolve(t, repo, Target{Kind: KindBranch, Branch: "late", Remote: "origin"})
	if p.Action != ActionTrackRemote {
		t.Fatalf("%+v", p)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Path, "two")); err != nil {
		t.Fatal("remote branch not checked out:", err)
	}
	if up := run(t, r.Path, "rev-parse", "--abbrev-ref", "@{u}"); up != "origin/late" {
		t.Fatal("not tracking:", up)
	}
}

func TestNewBranchStartsFromTheFreshRemote(t *testing.T) {
	repo, _, other := clone(t)
	newest := commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "main")
	before := run(t, repo, "rev-parse", "main")
	p := resolve(t, repo, Target{Kind: KindNew, Branch: "task"})
	if p.Action != ActionNewBranch || p.StartLabel != "origin/main" || p.Fetch == nil || filepath.Base(p.Path) != "task" || filepath.Base(filepath.Dir(p.Path)) != "app" {
		t.Fatalf("%+v", p)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if head := run(t, r.Path, "rev-parse", "HEAD"); head != newest {
		t.Fatal("did not start from the fetched default branch")
	}
	if run(t, repo, "rev-parse", "main") != before {
		t.Fatal("moved the local main")
	}
	if out, err := exec.Command("git", "-C", r.Path, "rev-parse", "--abbrev-ref", "@{u}").CombinedOutput(); err == nil {
		t.Fatal("new branch tracks something:", string(out))
	}
	// Doing the same again is refused, and says what to do.
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: "task"}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
}

func TestNewBranchFallsBackWhenTheRemoteIsUnreachable(t *testing.T) {
	repo, origin, _ := clone(t)
	p := resolve(t, repo, Target{Kind: KindNew, Branch: "task"})
	if err := os.Rename(origin, origin+".moved"); err != nil {
		t.Fatal(err)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Log) != 1 || !strings.Contains(r.Log[0], "last fetched copy") {
		t.Fatalf("%+v", r)
	}
}

func TestNewBranchBases(t *testing.T) {
	repo, _, other := clone(t)
	first := run(t, repo, "rev-parse", "HEAD")
	run(t, other, "checkout", "-q", "-b", "stack")
	stackTip := commit(t, other, "stack-file")
	run(t, other, "push", "-q", "origin", "stack")
	run(t, repo, "tag", "v1")
	commit(t, repo, "local-only")
	cases := []struct {
		base, label, want string
	}{
		{"v1", "v1", first},
		{"HEAD~1", "HEAD~1", first},
		{"origin/stack", "origin/stack", stackTip}, // Not fetched yet.
		{"stack", "origin/stack", stackTip},
		{first, first, first},
	}
	for i, tc := range cases {
		branch := "b" + string(rune('a'+i))
		p := resolve(t, repo, Target{Kind: KindNew, Branch: branch, Base: tc.base})
		if p.StartLabel != tc.label {
			t.Errorf("%s: label %q", tc.base, p.StartLabel)
		}
		r, err := Create(context.Background(), p)
		if err != nil {
			t.Fatal(tc.base, err)
		}
		if got := run(t, r.Path, "rev-parse", "HEAD"); got != tc.want {
			t.Errorf("%s: started at %s, want %s", tc.base, got, tc.want)
		}
	}
	for _, bad := range []string{"--orphan", "no-such-thing", "origin/no-such-thing"} {
		if _, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: "bad", Base: bad}, Options{Template: "/tmp/{branch}"}); err == nil {
			t.Error("accepted base", bad)
		}
	}
	for _, bad := range []string{"-x", "a b", "a..b", "HEAD", "x.lock", ""} {
		if _, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: bad}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "valid branch") {
			t.Errorf("accepted branch %q: %v", bad, err)
		}
	}
}

func TestNoCommitsYet(t *testing.T) {
	dir := tempDir(t)
	run(t, dir, "init", "-q", "-b", "main")
	_, err := Resolve(context.Background(), dir, Target{Kind: KindNew, Branch: "x"}, Options{Template: "/tmp/{branch}"})
	if err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Fatal(err)
	}
}

func TestExistingBranch(t *testing.T) {
	repo, _, other := clone(t)
	// A branch that is checked out somewhere attaches that checkout.
	main := resolve(t, repo, Target{Kind: KindBranch, Branch: "main"})
	if main.Action != ActionAttach || main.Path != repo {
		t.Fatalf("%+v", main)
	}
	wt := create(t, repo, Target{Kind: KindNew, Branch: "topic"})
	if p := resolve(t, repo, Target{Kind: KindBranch, Branch: "topic"}); p.Action != ActionAttach || p.Path != wt.Path {
		t.Fatalf("%+v", p)
	}
	// feature mustn't match feature/x.
	run(t, repo, "branch", "feature/x")
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindBranch, Branch: "feature"}, Options{Template: "/tmp/{branch}"}); err == nil {
		t.Fatal("a branch's prefix matched it")
	}
	// A free local branch gets a worktree, and fast-forwards to its upstream.
	run(t, repo, "branch", "--track", "behind", "origin/main")
	run(t, other, "push", "-q", "origin", "main:behind")
	run(t, repo, "fetch", "-q")
	commit(t, other, "two")
	tip := run(t, other, "rev-parse", "HEAD")
	run(t, other, "push", "-q", "origin", "HEAD:behind")
	run(t, repo, "config", "branch.behind.merge", "refs/heads/behind")
	p := resolve(t, repo, Target{Kind: KindBranch, Branch: "behind"})
	if p.Action != ActionLocalBranch || p.FastForward == nil {
		t.Fatalf("%+v", p)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if head := run(t, r.Path, "rev-parse", "HEAD"); head != tip || !slices.Contains(r.Log, "Fast-forwarded behind to origin/behind.") {
		t.Fatalf("%s %+v", head, r)
	}
	// One with unpushed commits isn't moved, and says so.
	run(t, repo, "branch", "ahead", "main")
	run(t, repo, "config", "branch.ahead.remote", "origin")
	run(t, repo, "config", "branch.ahead.merge", "refs/heads/behind")
	run(t, repo, "checkout", "-q", "ahead")
	local := commit(t, repo, "mine")
	run(t, repo, "checkout", "-q", "main")
	p = resolve(t, repo, Target{Kind: KindBranch, Branch: "ahead"})
	if p.FastForward != nil || len(p.Warnings) != 1 {
		t.Fatalf("%+v", p)
	}
	if r, err = Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if head := run(t, r.Path, "rev-parse", "HEAD"); head != local {
		t.Fatal("moved a branch with unpushed commits")
	}
	// A missing branch is said to be missing.
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindBranch, Branch: "missing"}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Fatal(err)
	}
}

func TestPullRequest(t *testing.T) {
	repo, origin, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "pr")
	tip := commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "pr")
	run(t, origin, "update-ref", "refs/pull/12/head", tip)
	run(t, origin, "update-ref", "-d", "refs/heads/pr")
	// With no gh, the number is enough, and that is mentioned.
	p := resolve(t, repo, Target{Kind: KindPullRequest, Number: 12})
	if p.Action != ActionDetached || filepath.Base(p.Path) != "pr-12" || len(p.Warnings) != 1 {
		t.Fatalf("%+v", p)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if head := run(t, r.Path, "rev-parse", "HEAD"); head != tip {
		t.Fatal(head)
	}
	if out, err := exec.Command("git", "-C", r.Path, "symbolic-ref", "-q", "HEAD").Output(); err == nil {
		t.Fatal("not detached:", string(out))
	}
	// It leaves no name behind, and nothing is lost by deleting it.
	if refs := run(t, repo, "for-each-ref", "refs/mu"); refs != "" {
		t.Fatal(refs)
	}
	// A second review gets a fresh folder.
	again := resolve(t, repo, Target{Kind: KindPullRequest, Number: 12})
	if again.Path == p.Path {
		t.Fatal("folder in use chosen again")
	}
	if err := CheckRemovable(context.Background(), repo, r.Path); err != nil {
		t.Fatal("couldn't delete a review with nothing added:", err)
	}
	// Work added on top of it is on no branch, so it is protected.
	commit(t, r.Path, "review-note")
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "no branch or tag") {
		t.Fatal(err)
	}
	// A pull request that gh doesn't know of is refused, as is one elsewhere, and
	// one the remote doesn't have.
	testutil.FakeGH(t, `echo 'GraphQL: Could not resolve to a PullRequest with the number of 99.' >&2; exit 1`)
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindPullRequest, Number: 99}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "wasn't found") {
		t.Fatal(err)
	}
	testutil.FakeGH(t, `echo '{"url":"https://github.com/someone/else/pull/5"}'`)
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindPullRequest, Number: 5}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "someone/else") {
		t.Fatal(err)
	}
	testutil.FakeGH(t, "exit 4")
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindPullRequest, Number: 404}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "wasn't found on origin") {
		t.Fatal(err)
	}
}

// fakePull makes gh describe a pull request of the clone's origin.
func fakePull(t *testing.T, origin, head string, fork bool) {
	t.Helper()
	repository := filepath.Base(filepath.Dir(origin)) + "/" + strings.TrimSuffix(filepath.Base(origin), ".git")
	testutil.FakeGH(t, fmt.Sprintf(`echo '{"url":"https://github.com/%s/pull/5","headRefName":%q,"isCrossRepository":%v}'`, repository, head, fork))
}

func TestPullRequestUsesItsBranch(t *testing.T) {
	repo, origin, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "feature/pr")
	tip := commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "feature/pr")
	run(t, origin, "update-ref", "refs/pull/5/head", tip)
	fakePull(t, origin, "feature/pr", false)

	// On the remote only: fetched and tracked, like any such branch, so it can be pushed.
	p := resolve(t, repo, Target{Kind: KindPullRequest, Number: 5})
	if p.Action != ActionTrackRemote || p.Branch != "feature/pr" || len(p.Warnings) != 0 {
		t.Fatalf("%+v", p)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if head := run(t, r.Path, "rev-parse", "HEAD"); head != tip {
		t.Fatal(head)
	}
	if upstream := run(t, r.Path, "rev-parse", "--abbrev-ref", "@{upstream}"); upstream != "origin/feature/pr" {
		t.Fatal(upstream)
	}
	if refs := run(t, repo, "for-each-ref", "refs/mu"); refs != "" {
		t.Fatal(refs)
	}
	// Checked out already: that checkout.
	if p := resolve(t, repo, Target{Kind: KindPullRequest, Number: 5}); p.Action != ActionAttach || p.Path != r.Path {
		t.Fatalf("%+v", p)
	}
	// A local branch of that name, free and with no upstream, is simply used.
	if err := Remove(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "branch", "--unset-upstream", "feature/pr")
	if p := resolve(t, repo, Target{Kind: KindPullRequest, Number: 5}); p.Action != ActionLocalBranch || p.Branch != "feature/pr" {
		t.Fatalf("%+v", p)
	}
}

func TestPullRequestWithoutAUsableBranchDetaches(t *testing.T) {
	repo, origin, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "gone")
	tip := commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "gone")
	run(t, origin, "update-ref", "refs/pull/5/head", tip)
	run(t, origin, "update-ref", "-d", "refs/heads/gone")

	// Its branch was deleted, as after merging.
	fakePull(t, origin, "gone", false)
	p := resolve(t, repo, Target{Kind: KindPullRequest, Number: 5})
	if p.Action != ActionDetached || p.Start != tip || len(p.Warnings) != 0 {
		t.Fatalf("%+v", p)
	}
	// From a fork: even a local branch of that name isn't this one.
	run(t, repo, "branch", "shared-name")
	fakePull(t, origin, "shared-name", true)
	p = resolve(t, repo, Target{Kind: KindPullRequest, Number: 5})
	if p.Action != ActionDetached || p.Start != tip {
		t.Fatalf("%+v", p)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := Read(context.Background(), repo)
	i := slices.IndexFunc(l.Worktrees, func(w Worktree) bool { return w.Path == r.Path })
	if i < 0 || l.Worktrees[i].Name() != "detached at "+tip[:7] {
		t.Fatalf("%+v", l.Worktrees)
	}
	if err := Remove(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteAddress(t *testing.T) {
	for url, host := range map[string]string{
		"https://github.com/me/app.git":       "github.com",
		"https://github.com/me/app":           "github.com",
		"git@github.com:me/app.git":           "github.com",
		"ssh://git@github.com/me/app.git":     "github.com",
		"https://GitHub.com/Me/App/":          "github.com",
		"git@github-work:me/app.git":          "github-work",
		"ssh://git@github.com:443/me/app.git": "github.com",
		"git@ssh.github.com:me/app.git":       "ssh.github.com",
	} {
		if h, r := remoteAddress(url); h != host || r != "me/app" {
			t.Errorf("%s: %s %s", url, h, r)
		}
	}
}

func TestPullRequestPrefersItsOwnHost(t *testing.T) {
	repo, _, _ := clone(t)
	ctx := context.Background()
	run(t, repo, "remote", "set-url", "origin", "git@github.com:acme/widgets.git")
	run(t, repo, "remote", "add", "alias", "git@a-host-first-in-the-list:acme/widgets.git")
	remotes := func() Listing { return Listing{Root: repo, Remotes: []string{"alias", "origin"}} }
	if remote := remoteHosting(ctx, remotes(), "github.com", "acme/widgets"); remote != "origin" {
		t.Fatal(remote)
	}
	// With no remote on that host, an alias for it is the best there is.
	run(t, repo, "remote", "remove", "origin")
	if remote := remoteHosting(ctx, Listing{Root: repo, Remotes: []string{"alias"}}, "github.com", "acme/widgets"); remote != "alias" {
		t.Fatal(remote)
	}
}

func TestDestination(t *testing.T) {
	repo, _, _ := clone(t)
	parent := filepath.Dir(repo)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for template, want := range map[string]string{
		"{checkoutParent}/{repo}.{branch}":    filepath.Join(parent, "app.a-b"),
		"{repoPath}/.worktrees/{branch}":      filepath.Join(repo, ".worktrees", "a-b"),
		"../{repo}.{branch}":                  filepath.Join(parent, "app.a-b"),
		"~/worktrees-mu-test/{repo}/{branch}": filepath.Join(home, "worktrees-mu-test", "app", "a-b"),
	} {
		p, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: "a/b"}, Options{Template: template})
		if err != nil {
			t.Fatal(template, err)
		}
		if p.Path != want {
			t.Errorf("%s: %s, want %s", template, p.Path, want)
		}
	}
	for _, bad := range []string{"{nope}/x", "/x/{branch", "/x/", "/x/..", "~root/x"} {
		if err := ValidateTemplate(bad); err == nil {
			t.Error("accepted template", bad)
		}
	}
	// Occupied folders get a suffix; an empty one counts as occupied.
	occupied := filepath.Join(parent, "wt", "app", "busy")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if p := resolve(t, repo, Target{Kind: KindNew, Branch: "busy"}); p.Path != occupied+"-2" {
		t.Fatal(p.Path)
	}
	// A path git still has registered, with its folder gone, also counts.
	gone := create(t, repo, Target{Kind: KindNew, Branch: "gone"})
	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}
	if p := resolve(t, repo, Target{Kind: KindNew, Branch: "gone2"}); p.Path == gone.Path {
		t.Fatal(p.Path)
	}
	run(t, repo, "branch", "-m", "gone", "renamed")
	if p := resolve(t, repo, Target{Kind: KindNew, Branch: "gone"}); p.Path != gone.Path+"-2" {
		t.Fatal(p.Path)
	}
	// A folder that appeared after planning, even an empty one, isn't used either.
	p := resolve(t, repo, Target{Kind: KindNew, Branch: "late"})
	if err := os.MkdirAll(p.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if r, err := Create(context.Background(), p); err != nil || r.Path != p.Path+"-2" {
		t.Fatal(r, err)
	}
}

func TestSymlinkedParentIsResolved(t *testing.T) {
	repo, _, _ := clone(t)
	real := tempDir(t)
	link := filepath.Join(tempDir(t), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	p, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: "x"}, Options{Template: link + "/{branch}"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := Read(context.Background(), repo)
	if r.Path != filepath.Join(real, "x") || !slices.ContainsFunc(l.Worktrees, func(w Worktree) bool { return w.Path == r.Path }) {
		t.Fatalf("%s not as git lists it: %+v", r.Path, l.Worktrees)
	}
}

func TestProjectInASubfolder(t *testing.T) {
	repo, _, other := clone(t)
	commit(t, other, "pkg/lib/file")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "pull", "-q")
	sub := filepath.Join(repo, "pkg", "lib")
	l, err := Read(context.Background(), sub)
	if err != nil || l.Root != repo || l.Prefix != filepath.Join("pkg", "lib") {
		t.Fatalf("%+v %v", l, err)
	}
	p := resolve(t, sub, Target{Kind: KindNew, Branch: "x"})
	p.Path = filepath.Join(tempDir(t), "x")
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Cwd != filepath.Join(r.Path, "pkg", "lib") {
		t.Fatal(r.Cwd)
	}
	// A base without that folder works from the top of the checkout.
	first := run(t, repo, "rev-list", "--max-parents=0", "HEAD")
	p = resolve(t, sub, Target{Kind: KindNew, Branch: "y", Base: first})
	r, err = Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Cwd != r.Path {
		t.Fatal(r.Cwd)
	}
}

func TestProjectInALinkedWorktree(t *testing.T) {
	repo, _, _ := clone(t)
	linked := create(t, repo, Target{Kind: KindNew, Branch: "linked"})
	l, err := Read(context.Background(), linked.Path)
	if err != nil || l.Root != linked.Path || len(l.Worktrees) != 2 {
		t.Fatalf("%+v %v", l, err)
	}
	// {repo} is still the primary checkout's name.
	if p := resolve(t, linked.Path, Target{Kind: KindNew, Branch: "x"}); filepath.Base(filepath.Dir(p.Path)) != "app" {
		t.Fatal(p.Path)
	}
}

func TestProvisioning(t *testing.T) {
	repo, _, other := clone(t)
	if err := os.WriteFile(filepath.Join(other, ".gitignore"), []byte(".env\nconfig/\nlink\npresent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, other, "add", ".gitignore")
	if err := os.WriteFile(filepath.Join(other, ".worktreeinclude"), []byte(".env\nconfig/\nlink\npresent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, other, "add", ".worktreeinclude")
	commit(t, other, "tracked")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "pull", "-q")
	write := func(name, content string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".env", "SECRET=1", 0o600)
	write("config/local.json", "{}", 0o644)
	secret := filepath.Join(tempDir(t), "secret")
	if err := os.WriteFile(secret, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(repo, "link")); err != nil {
		t.Skip(err)
	}
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	got, err := os.ReadFile(filepath.Join(r.Path, ".env"))
	if err != nil || string(got) != "SECRET=1" {
		t.Fatal(string(got), err)
	}
	if info, _ := os.Stat(filepath.Join(r.Path, ".env")); info.Mode().Perm() != 0o600 {
		t.Fatal("mode not kept:", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(r.Path, "config", "local.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(r.Path, "link")); err == nil {
		t.Fatal("followed or copied a symlink")
	}
	if len(r.Log) != 1 || !strings.Contains(r.Log[0], "Copied 2 files") || !strings.Contains(r.Log[0], "Skipped 1") {
		t.Fatalf("%+v", r.Log)
	}
	// Attaching provisions nothing and changes nothing.
	if err := os.Remove(filepath.Join(r.Path, ".env")); err != nil {
		t.Fatal(err)
	}
	if a := create(t, repo, Target{Kind: KindPath, Path: r.Path}); a.Path != r.Path || len(a.Log) != 0 {
		t.Fatalf("%+v", a)
	}
	if _, err := os.Stat(filepath.Join(r.Path, ".env")); err == nil {
		t.Fatal("attaching copied files")
	}
}

func TestSubmodules(t *testing.T) {
	repo, _, other := clone(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	library := filepath.Join(tempDir(t), "library")
	run(t, filepath.Dir(library), "init", "-q", "-b", "main", library)
	commit(t, library, "lib-file")
	run(t, other, "submodule", "add", "-q", library, "lib")
	run(t, other, "commit", "-q", "-m", "submodule")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "pull", "-q")
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	if _, err := os.Stat(filepath.Join(r.Path, "lib", "lib-file")); err != nil {
		t.Fatal("submodule not initialized:", err)
	}
	// Dirty inside the submodule counts as dirty.
	if err := os.WriteFile(filepath.Join(r.Path, "lib", "new"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), repo, r.Path); err == nil {
		t.Fatal("removed a worktree with work in its submodule")
	}
	if err := os.Remove(filepath.Join(r.Path, "lib", "new")); err != nil {
		t.Fatal(err)
	}
	// Git refuses any worktree with submodules unless forced; a clean one goes.
	if err := Remove(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
	if exists(r.Path) {
		t.Fatal("still there")
	}
}

func TestRemoval(t *testing.T) {
	repo, _, _ := clone(t)
	ctx := context.Background()
	if err := CheckRemovable(ctx, repo, repo); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatal(err)
	}
	if err := CheckRemovable(ctx, repo, "/not/a/worktree"); err == nil {
		t.Fatal("accepted a path that isn't a worktree")
	}
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	if err := CheckRemovable(ctx, repo, r.Path); err != nil {
		t.Fatal(err)
	}
	// Untracked files and changes both stop it, and nothing is touched.
	file := filepath.Join(r.Path, "work")
	if err := os.WriteFile(file, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckRemovable(ctx, repo, r.Path); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatal(err)
	}
	if err := Remove(ctx, repo, r.Path); err == nil {
		t.Fatal("removed a worktree with an untracked file")
	}
	if !exists(file) {
		t.Fatal("lost work")
	}
	// Committed work stays on its branch, so the worktree can go.
	run(t, r.Path, "add", "work")
	run(t, r.Path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "work")
	tip := run(t, r.Path, "rev-parse", "HEAD")
	if err := CheckRemovable(ctx, repo, r.Path); err != nil {
		t.Fatal(err)
	}
	// A lock stops it.
	run(t, repo, "worktree", "lock", r.Path)
	if err := CheckRemovable(ctx, repo, r.Path); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatal(err)
	}
	run(t, repo, "worktree", "unlock", r.Path)
	if err := Remove(ctx, repo, r.Path); err != nil {
		t.Fatal(err)
	}
	if exists(r.Path) || run(t, repo, "rev-parse", "x") != tip {
		t.Fatal("folder stayed, or branch lost")
	}
	// An unmerged branch is kept; a merged one goes.
	if err := DeleteBranch(ctx, repo, "x"); err == nil {
		t.Fatal("deleted an unmerged branch")
	}
	if run(t, repo, "rev-parse", "x") != tip {
		t.Fatal("branch lost")
	}
	run(t, repo, "branch", "merged", "main")
	if err := DeleteBranch(ctx, repo, "merged"); err != nil {
		t.Fatal(err)
	}
	// One whose folder was deleted by hand can be forgotten.
	gone := create(t, repo, Target{Kind: KindNew, Branch: "gone"})
	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}
	l, _ := Read(ctx, repo)
	if i := slices.IndexFunc(l.Worktrees, func(w Worktree) bool { return w.Path == gone.Path }); i < 0 || !l.Worktrees[i].Missing {
		t.Fatalf("%+v", l.Worktrees)
	}
	if _, err := Resolve(ctx, repo, Target{Kind: KindPath, Path: gone.Path}, Options{Template: "/tmp/{branch}"}); err == nil {
		t.Fatal("attached a worktree whose folder is gone")
	}
	if _, err := Resolve(ctx, repo, Target{Kind: KindBranch, Branch: "gone"}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatal(err)
	}
	if err := CheckRemovable(ctx, repo, gone.Path); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, repo, gone.Path); err != nil {
		t.Fatal(err)
	}
	if l, _ = Read(ctx, repo); len(l.Worktrees) != 1 {
		t.Fatalf("%+v", l.Worktrees)
	}
}

func TestDetachedCommitsOnNoBranchAreProtected(t *testing.T) {
	repo, _, _ := clone(t)
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	run(t, r.Path, "checkout", "-q", "--detach")
	if err := CheckRemovable(context.Background(), repo, r.Path); err != nil {
		t.Fatal("detached at a commit a branch has:", err)
	}
	commit(t, r.Path, "only-here")
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "no branch or tag") {
		t.Fatal(err)
	}
}

func TestAnExpiredCreationRecordDoesntHideDetachedWork(t *testing.T) {
	repo, origin, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "gone")
	tip := commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "gone")
	run(t, origin, "update-ref", "refs/pull/12/head", tip)
	run(t, origin, "update-ref", "-d", "refs/heads/gone")
	r := create(t, repo, Target{Kind: KindPullRequest, Number: 12})
	commit(t, r.Path, "private-work")
	logs := run(t, r.Path, "rev-parse", "--git-path", "logs/HEAD")
	if !filepath.IsAbs(logs) {
		logs = filepath.Join(r.Path, logs)
	}
	data, err := os.ReadFile(logs)
	if err != nil {
		t.Fatal(err)
	}
	// What expiring the creation record leaves: only the commit, as the one entry.
	_, kept, ok := strings.Cut(strings.TrimRight(string(data), "\n"), "\n")
	if !ok {
		t.Fatalf("%q", data)
	}
	if err := os.WriteFile(logs, []byte(kept+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "no branch or tag") {
		t.Fatal(err)
	}
}

func TestContains(t *testing.T) {
	for _, tc := range []struct {
		dir, path string
		want      bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b", "/a/b/..c", true},
	} {
		if got := Contains(tc.dir, tc.path); got != tc.want {
			t.Errorf("%+v: %v", tc, got)
		}
	}
}

func TestSuggestBranchIsValid(t *testing.T) {
	repo, _, _ := clone(t)
	a, b := SuggestBranch(), SuggestBranch()
	if a == b || !validBranchName(context.Background(), repo, a) {
		t.Fatal(a, b)
	}
}

func TestNamesThatMeanSomethingElse(t *testing.T) {
	repo, _, _ := clone(t)
	run(t, repo, "branch", "feature")
	run(t, repo, "branch", "nested/deep")
	for name, want := range map[string]string{
		"refs/heads/rh": "valid branch",
		"@":             "valid branch",
		"feature/x":     "alongside feature",
		"nested":        "alongside nested/deep",
	} {
		if _, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: name}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A name that reads like a remote's branch can be meant: a remote can be named
	// after someone, whose name then prefixes a new branch. So it only warns.
	if p := resolve(t, repo, Target{Kind: KindNew, Branch: "origin/fix"}); len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "on origin") {
		t.Fatalf("%+v", p)
	}
	// A remote branch can't be tracked alongside a local one in its way.
	run(t, repo, "branch", "elsewhere")
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindBranch, Branch: "elsewhere/child", Remote: "origin"}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "alongside") {
		t.Fatal(err)
	}
}

func TestRemoteDefaultBranchMayHaveMoved(t *testing.T) {
	repo, origin, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "develop")
	tip := commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "develop")
	run(t, origin, "symbolic-ref", "HEAD", "refs/heads/develop")
	l, err := Read(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	head := ReadRemote(context.Background(), l).Head
	if head != "develop" {
		t.Fatal(head)
	}
	p, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: "x"}, Options{Template: filepath.Join(tempDir(t), "{branch}"), RemoteHead: head})
	if err != nil {
		t.Fatal(err)
	}
	if p.StartLabel != "origin/develop" {
		t.Fatal(p.StartLabel)
	}
	r, err := Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, r.Path, "rev-parse", "HEAD"); got != tip {
		t.Fatal(got)
	}
}

func TestHiddenChangesCount(t *testing.T) {
	repo, _, other := clone(t)
	commit(t, other, "tracked")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "pull", "-q")
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		r := create(t, repo, Target{Kind: KindNew, Branch: "b" + flag})
		run(t, r.Path, "update-index", flag, "tracked")
		if err := os.WriteFile(filepath.Join(r.Path, "tracked"), []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "update-index") {
			t.Fatal(flag, err)
		}
	}
	// In a sparse checkout, that is how files are left out.
	r := create(t, repo, Target{Kind: KindNew, Branch: "sparse"})
	run(t, r.Path, "sparse-checkout", "set", "--no-cone", "/one")
	if err := CheckRemovable(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreesCantGoInAFile(t *testing.T) {
	repo, _, _ := clone(t)
	file := filepath.Join(tempDir(t), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: "x"}, Options{Template: file + "/sub/{branch}"}); err == nil || !strings.Contains(err.Error(), "isn't a folder") {
		t.Fatal(err)
	}
}

func TestATagOnlyOnTheRemoteNeedsFetching(t *testing.T) {
	repo, _, other := clone(t)
	run(t, other, "tag", "remote-tag")
	run(t, other, "push", "-q", "origin", "remote-tag")
	for _, base := range []string{"remote-tag", "origin/remote-tag"} {
		if _, err := Resolve(context.Background(), repo, Target{Kind: KindNew, Branch: "x", Base: base}, Options{Template: "/tmp/{branch}"}); err == nil || !strings.Contains(err.Error(), "fetched") {
			t.Fatal(base, err)
		}
	}
	if tags := run(t, repo, "tag", "-l", "remote-tag"); tags != "" {
		t.Fatal(tags)
	}
	run(t, repo, "fetch", "-q", "origin", "tag", "remote-tag")
	if p := resolve(t, repo, Target{Kind: KindNew, Branch: "x", Base: "remote-tag"}); p.StartLabel != "remote-tag" || p.Fetch != nil {
		t.Fatalf("%+v", p)
	}
}

func TestTrackingWorksInASingleBranchClone(t *testing.T) {
	_, origin, other := clone(t)
	run(t, other, "checkout", "-q", "-b", "elsewhere")
	tip := commit(t, other, "two")
	run(t, other, "push", "-q", "origin", "elsewhere")
	repo := filepath.Join(tempDir(t), "single")
	run(t, filepath.Dir(repo), "clone", "-q", "--single-branch", "file://"+origin, repo)
	r := create(t, repo, Target{Kind: KindBranch, Branch: "elsewhere", Remote: "origin"})
	if head := run(t, r.Path, "rev-parse", "HEAD"); head != tip {
		t.Fatal(head)
	}
	if upstream := run(t, r.Path, "rev-parse", "--abbrev-ref", "@{upstream}"); upstream != "origin/elsewhere" {
		t.Fatal(upstream)
	}
}

func TestFailingHookSaysWhatStayed(t *testing.T) {
	repo, _, _ := clone(t)
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := resolve(t, repo, Target{Kind: KindNew, Branch: "x"})
	_, err := Create(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "perhaps in a hook") || !strings.Contains(err.Error(), "stays at "+p.Path) || strings.Contains(err.Error(), "Preparing worktree") {
		t.Fatal(err)
	}
}

func TestAPullRequestCantChooseWhatIsCopied(t *testing.T) {
	repo, origin, other := clone(t)
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, other, "checkout", "-q", "-b", "evil")
	if err := os.WriteFile(filepath.Join(other, ".worktreeinclude"), []byte("*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, other, "add", ".worktreeinclude")
	run(t, other, "commit", "-q", "-m", "include")
	run(t, other, "push", "-q", "origin", "evil")
	run(t, origin, "update-ref", "refs/pull/9/head", run(t, other, "rev-parse", "HEAD"))
	r := create(t, repo, Target{Kind: KindPullRequest, Number: 9})
	if exists(filepath.Join(r.Path, ".env")) {
		t.Fatal("a pull request's own file copied a secret into its worktree")
	}
	if len(r.Log) != 1 || !strings.Contains(r.Log[0], "code under review") {
		t.Fatalf("%+v", r.Log)
	}
}

func TestUnpushedSubmoduleWorkIsProtected(t *testing.T) {
	repo, _, other := clone(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	library := filepath.Join(tempDir(t), "library")
	run(t, filepath.Dir(library), "init", "-q", "-b", "main", library)
	commit(t, library, "lib-file")
	run(t, other, "submodule", "add", "-q", library, "lib")
	run(t, other, "commit", "-q", "-m", "submodule")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "pull", "-q")
	ctx := context.Background()
	refused := func(path, want string) {
		t.Helper()
		if err := Remove(ctx, repo, path); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatal(err)
		}
		if !exists(path) {
			t.Fatal("lost the worktree")
		}
	}

	// A commit in the submodule, recorded by the worktree's branch.
	recorded := create(t, repo, Target{Kind: KindNew, Branch: "recorded"})
	sub := filepath.Join(recorded.Path, "lib")
	run(t, sub, "checkout", "-q", "-b", "work")
	commit(t, sub, "work")
	run(t, recorded.Path, "add", "lib")
	run(t, recorded.Path, "commit", "-q", "-m", "bump")
	refused(recorded.Path, "aren't on its remote")

	// A local branch's commit, with the submodule back where it was recorded.
	back := create(t, repo, Target{Kind: KindNew, Branch: "back"})
	sub = filepath.Join(back.Path, "lib")
	head := run(t, sub, "rev-parse", "HEAD")
	run(t, sub, "checkout", "-q", "-b", "work")
	commit(t, sub, "work")
	run(t, sub, "checkout", "-q", head)
	refused(back.Path, "aren't on its remote")

	// Dirty, with configuration hiding it from a plain git status.
	hidden := create(t, repo, Target{Kind: KindNew, Branch: "hidden"})
	run(t, hidden.Path, "config", "--local", "diff.ignoreSubmodules", "all")
	if err := os.WriteFile(filepath.Join(hidden.Path, "lib", "new"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	refused(hidden.Path, "uncommitted")
}

// submoduleRepo makes a clone whose repository has a submodule at lib, of a library
// where the commit to record comes from fix, and returns the clone and the library.
func submoduleRepo(t *testing.T, prepare func(library string) string) (repo string) {
	t.Helper()
	repo, _, other := clone(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	library := filepath.Join(tempDir(t), "library")
	run(t, filepath.Dir(library), "init", "-q", "-b", "main", library)
	commit(t, library, "lib-file")
	record := prepare(library)
	run(t, other, "submodule", "add", "-q", library, "lib")
	run(t, filepath.Join(other, "lib"), "fetch", "-q", "origin", record)
	run(t, filepath.Join(other, "lib"), "checkout", "-q", record)
	run(t, other, "add", "lib")
	run(t, other, "commit", "-q", "-m", "submodule")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "pull", "-q")
	return repo
}

func TestUntouchedSubmodulesDontStopDeletion(t *testing.T) {
	for name, prepare := range map[string]func(library string) string{
		"at the remote's tip": func(library string) string { return run(t, library, "rev-parse", "HEAD") },
		"an unrelated tag on no branch": func(library string) string {
			head := run(t, library, "rev-parse", "HEAD")
			run(t, library, "checkout", "-q", "--detach")
			commit(t, library, "off-branch")
			run(t, library, "tag", "old")
			run(t, library, "checkout", "-q", "main")
			return head
		},
		"recorded only on a tag": func(library string) string {
			run(t, library, "checkout", "-q", "--detach")
			tip := commit(t, library, "tagged")
			run(t, library, "tag", "release")
			run(t, library, "checkout", "-q", "main")
			return tip
		},
		"recorded only on a pull request's ref": func(library string) string {
			run(t, library, "checkout", "-q", "--detach")
			tip := commit(t, library, "pr")
			run(t, library, "update-ref", "refs/pull/1/head", tip)
			run(t, library, "checkout", "-q", "main")
			run(t, library, "config", "uploadpack.allowAnySHA1InWant", "true")
			return tip
		},
	} {
		t.Run(name, func(t *testing.T) {
			repo := submoduleRepo(t, prepare)
			r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
			if !exists(filepath.Join(r.Path, "lib", "lib-file")) {
				t.Fatalf("not initialized: %+v", r)
			}
			if err := Remove(context.Background(), repo, r.Path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSubmoduleTagsDontProveAnything(t *testing.T) {
	repo := submoduleRepo(t, func(library string) string { return run(t, library, "rev-parse", "HEAD") })
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	sub := filepath.Join(r.Path, "lib")
	head := run(t, sub, "rev-parse", "HEAD")
	run(t, sub, "checkout", "-q", "--detach")
	commit(t, sub, "tagged-here")
	run(t, sub, "tag", "local-only")
	// At that commit, recorded by an unpublished commit of the repository.
	run(t, r.Path, "add", "lib")
	run(t, r.Path, "commit", "-q", "-m", "bump")
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "aren't on its remote") {
		t.Fatal(err)
	}
	// Moved away, with only the tag left holding the commit.
	run(t, sub, "checkout", "-q", head)
	run(t, r.Path, "add", "lib")
	run(t, r.Path, "commit", "-q", "-m", "back")
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "aren't on its remote") {
		t.Fatal(err)
	}
	run(t, sub, "tag", "-d", "local-only")
	if err := CheckRemovable(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
}

func TestSubmoduleLayoutsThatForeachMisses(t *testing.T) {
	repo := submoduleRepo(t, func(library string) string { return run(t, library, "rev-parse", "HEAD") })

	// Deinitialized, with an unpublished branch kept in its repository.
	deinit := create(t, repo, Target{Kind: KindNew, Branch: "deinit"})
	sub := filepath.Join(deinit.Path, "lib")
	head := run(t, sub, "rev-parse", "HEAD")
	run(t, sub, "checkout", "-q", "-b", "work")
	commit(t, sub, "work")
	run(t, sub, "checkout", "-q", head)
	run(t, deinit.Path, "submodule", "deinit", "-q", "--all")
	if err := CheckRemovable(context.Background(), repo, deinit.Path); err == nil || !strings.Contains(err.Error(), "aren't on its remote") {
		t.Fatal(err)
	}

	// Deinitialized with nothing of its own, it can go.
	clean := create(t, repo, Target{Kind: KindNew, Branch: "clean"})
	run(t, clean.Path, "submodule", "deinit", "-q", "--all")
	if err := Remove(context.Background(), repo, clean.Path); err != nil {
		t.Fatal(err)
	}

	// The old layout, with the repository inside the submodule's folder.
	old := create(t, repo, Target{Kind: KindNew, Branch: "old"})
	sub = filepath.Join(old.Path, "lib")
	head = run(t, sub, "rev-parse", "HEAD")
	gitDir := run(t, sub, "rev-parse", "--absolute-git-dir")
	if err := os.Remove(filepath.Join(sub, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(gitDir, filepath.Join(sub, ".git")); err != nil {
		t.Fatal(err)
	}
	run(t, old.Path, "config", "--file", filepath.Join(sub, ".git", "config"), "--unset", "core.worktree")
	run(t, sub, "checkout", "-q", "-b", "work")
	commit(t, sub, "work")
	run(t, sub, "checkout", "-q", head)
	if err := CheckRemovable(context.Background(), repo, old.Path); err == nil || !strings.Contains(err.Error(), "aren't on its remote") {
		t.Fatal(err)
	}
}

func TestSubmoduleWithItsOwnWorktreesIsProtected(t *testing.T) {
	repo := submoduleRepo(t, func(library string) string { return run(t, library, "rev-parse", "HEAD") })
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	run(t, filepath.Join(r.Path, "lib"), "worktree", "add", "-q", "--detach", filepath.Join(tempDir(t), "external"))
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "worktrees of its own") {
		t.Fatal(err)
	}
}

func TestAFirstRunAsksAboutTagsAndLaterOnesDont(t *testing.T) {
	var library string
	repo := submoduleRepo(t, func(lib string) string {
		library = lib
		head := run(t, lib, "rev-parse", "HEAD")
		run(t, lib, "checkout", "-q", "--detach")
		commit(t, lib, "off-branch")
		run(t, lib, "tag", "old")
		run(t, lib, "checkout", "-q", "main")
		return head
	})
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	ctx := context.Background()
	check := &RemovalCheck{}
	if err := check.Run(ctx, repo, r.Path); err != nil {
		t.Fatal(err)
	}
	// With the remote gone, the answer it gave stands, for as long as this check lasts.
	if err := os.Rename(library, library+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := check.Run(ctx, repo, r.Path); err != nil {
		t.Fatal(err)
	}
	// A fresh check can't tell, and refuses.
	if err := CheckRemovable(ctx, repo, r.Path); err == nil || !strings.Contains(err.Error(), "couldn't be asked") {
		t.Fatal(err)
	}
	// One that hasn't asked never does, once it has run.
	empty := &RemovalCheck{ran: true}
	if err := empty.Run(ctx, repo, r.Path); err == nil || !strings.Contains(err.Error(), "couldn't be asked") {
		t.Fatal(err)
	}
	if err := check.Remove(ctx, repo, r.Path); err != nil {
		t.Fatal(err)
	}
}

func TestGitlinkWithoutAnEntryDoesntStopDeletion(t *testing.T) {
	repo, _, other := clone(t)
	run(t, other, "update-index", "--add", "--cacheinfo", "160000,"+run(t, other, "rev-parse", "HEAD")+",phantom")
	run(t, other, "commit", "-q", "-m", "phantom")
	run(t, other, "push", "-q", "origin", "main")
	run(t, repo, "pull", "-q")
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	if err := Remove(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
}

func TestUntrackedFilesCountWhateverTheConfiguration(t *testing.T) {
	repo, _, _ := clone(t)
	run(t, repo, "config", "status.showUntrackedFiles", "no")
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	if err := os.WriteFile(filepath.Join(r.Path, "precious"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatal(err)
	}
}

func TestOnlyLastingRefsProtectDetachedCommits(t *testing.T) {
	repo, _, _ := clone(t)
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	run(t, r.Path, "checkout", "-q", "--detach")
	commit(t, r.Path, "only-here")
	run(t, r.Path, "update-ref", "refs/worktree/wip", "HEAD")
	run(t, r.Path, "update-ref", "refs/stash", "HEAD")
	if err := CheckRemovable(context.Background(), repo, r.Path); err == nil || !strings.Contains(err.Error(), "no branch or tag") {
		t.Fatal(err)
	}
	run(t, r.Path, "tag", "kept")
	if err := CheckRemovable(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
}

func TestCancellingLetsACheckoutFinish(t *testing.T) {
	repo, _, _ := clone(t)
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := resolve(t, repo, Target{Kind: KindNew, Branch: "x"})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	if _, err := Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	l, _ := Read(context.Background(), repo)
	if i := slices.IndexFunc(l.Worktrees, func(w Worktree) bool { return w.Path == p.Path }); i < 0 || l.Worktrees[i].Locked || l.Worktrees[i].Missing {
		t.Fatalf("%+v", l.Worktrees)
	}
}

func TestEndingGitEndsWhatItStarted(t *testing.T) {
	repo, _, _ := clone(t)
	old := settleGrace
	settleGrace = 100 * time.Millisecond
	defer func() { settleGrace = old }()
	pidFile := filepath.Join(tempDir(t), "pid")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 60 &\necho $! > "+pidFile+"\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := resolve(t, repo, Target{Kind: KindNew, Branch: "x"})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	if _, err := Create(ctx, p); err == nil {
		t.Fatal("a hook that hung didn't stop it")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid := strings.TrimSpace(string(data))
	deadline := time.Now().Add(5 * time.Second)
	for exec.Command("kill", "-0", pid).Run() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the hook's child outlived git")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAddChecksAnAttachedWorktreeIsStillThere(t *testing.T) {
	repo, _, _ := clone(t)
	r := create(t, repo, Target{Kind: KindNew, Branch: "x"})
	p := resolve(t, repo, Target{Kind: KindBranch, Branch: "x"})
	if p.Action != ActionAttach {
		t.Fatalf("%+v", p)
	}
	if err := Remove(context.Background(), repo, r.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(context.Background(), p); err == nil {
		t.Fatal("attached a worktree that has gone")
	}
}

func TestAnInterruptedOrUnrecognizedWorktreeCantBeUsed(t *testing.T) {
	repo, _, _ := clone(t)
	ctx := context.Background()
	options := Options{Template: filepath.Join(tempDir(t), "{branch}")}
	interrupted := create(t, repo, Target{Kind: KindNew, Branch: "interrupted"})
	run(t, repo, "worktree", "lock", "--reason", "initializing", interrupted.Path)
	if _, err := Resolve(ctx, repo, Target{Kind: KindPath, Path: interrupted.Path}, options); err == nil || !strings.Contains(err.Error(), "never finished") {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, repo, Target{Kind: KindBranch, Branch: "interrupted"}, options); err == nil || !strings.Contains(err.Error(), "never finished") {
		t.Fatal(err)
	}
	broken := create(t, repo, Target{Kind: KindNew, Branch: "broken"})
	if err := os.Remove(filepath.Join(broken.Path, ".git")); err != nil {
		t.Fatal(err)
	}
	l, err := Read(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(l.Worktrees, func(w Worktree) bool { return w.Path == broken.Path })
	if i < 0 || !l.Worktrees[i].Broken || l.Worktrees[i].Missing {
		t.Fatalf("%+v", l.Worktrees)
	}
	if err := CheckRemovable(ctx, repo, broken.Path); err == nil || !strings.Contains(err.Error(), "no longer recognizes") {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, repo, Target{Kind: KindPath, Path: broken.Path}, options); err == nil {
		t.Fatal("used a worktree git doesn't recognize")
	}
}
