package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/mu/internal/testutil"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func awaitBranch(t *testing.T, a *App, id, branch string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if findThread(t, a, id).Git.Branch == branch {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("branch %q, want %q", findThread(t, a, id).Git.Branch, branch)
}

func TestFocusedThreadShowsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	other := createTest(t, a, testutil.Agent(t))
	dir := findThread(t, a, id).Cwd
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, findThread(t, a, other).Cwd, "init", "-q", "-b", "unfocused")

	// Only the thread in view is read.
	a.FocusThread(id)
	awaitBranch(t, a, id, "main", 2*time.Second)
	if v := findThread(t, a, other); v.Git.Repo() {
		t.Fatalf("read a thread out of view: %+v", v.Git)
	}

	// The end of a turn reads it again at once, well before the periodic reading.
	gitIn(t, dir, "checkout", "-q", "-b", "during-turn")
	sendDone(t, a, id, "hello")
	awaitBranch(t, a, id, "during-turn", workspaceRefresh/2)

	// A change outside any turn appears with the periodic reading.
	gitIn(t, dir, "checkout", "-q", "-b", "between-turns")
	awaitBranch(t, a, id, "between-turns", workspaceRefresh+2*time.Second)

	// A folder that stops being a repository stops showing it.
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	a.FocusThread(other)
	awaitBranch(t, a, other, "unfocused", 2*time.Second)
	a.FocusThread(id)
	awaitBranch(t, a, id, "", 2*time.Second)
	if v := findThread(t, a, id); v.Git.Repo() {
		t.Fatalf("stale repository: %+v", v.Git)
	}
}

func TestUnknownReadingKeepsTheLast(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	dir := findThread(t, a, id).Cwd
	gitIn(t, dir, "init", "-q", "-b", "main")
	a.FocusThread(id)
	awaitBranch(t, a, id, "main", 2*time.Second)
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.readWorkspace()
	if got := findThread(t, a, id).Git.Branch; got != "main" {
		t.Fatalf("an unreadable repository cleared the reading: %q", got)
	}
}

// prScript is a gh that answers by the folder's branch, unless the file named
// fail exists, and logs each call.
func prScript(log, fail string) string {
	return `echo call >> '` + log + `'
if [ -e '` + fail + `' ]; then echo 'error connecting to api.github.com' >&2; exit 1; fi
case "$(git symbolic-ref --short HEAD)" in
main) echo '{"number":7,"state":"OPEN","isDraft":false,"title":"Seven"}' ;;
draft) echo '{"number":8,"state":"OPEN","isDraft":true,"title":"Eight"}' ;;
*) echo 'no pull requests found' >&2; exit 1 ;;
esac`
}

func awaitPullRequest(t *testing.T, a *App, id string, number int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if findThread(t, a, id).PullRequest.Number == number {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("pull request %d, want %d", findThread(t, a, id).PullRequest.Number, number)
}

func TestPullRequestFollowsTheBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	scratch := t.TempDir()
	log, fail := filepath.Join(scratch, "log"), filepath.Join(scratch, "fail")
	testutil.FakeGH(t, prScript(log, fail))
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	plain := createTest(t, a, testutil.Agent(t))
	dir := findThread(t, a, id).Cwd
	gitIn(t, dir, "init", "-q", "-b", "main")

	a.FocusThread(id)
	awaitPullRequest(t, a, id, 7)
	if pr := findThread(t, a, id).PullRequest; pr.State != "OPEN" || pr.Title != "Seven" {
		t.Fatalf("%+v", pr)
	}

	// A branch without one shows none, not the last branch's.
	gitIn(t, dir, "checkout", "-q", "-b", "no-pull-request")
	sendDone(t, a, id, "hello")
	awaitBranch(t, a, id, "no-pull-request", 2*time.Second)
	if pr := findThread(t, a, id).PullRequest; pr.Found() {
		t.Fatalf("another branch's pull request: %+v", pr)
	}

	// A new branch found by the periodic reading asks at once.
	gitIn(t, dir, "checkout", "-q", "-b", "draft")
	deadline := time.Now().Add(workspaceRefresh + 2*time.Second)
	for findThread(t, a, id).PullRequest.Number != 8 {
		if time.Now().After(deadline) {
			t.Fatal("a checkout between turns did not ask about the new branch")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// GitHub out of reach is an unknown answer: the last one stays.
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a.readPullRequest()
	if pr := findThread(t, a, id).PullRequest; pr.Number != 8 {
		t.Fatalf("a failure cleared the answer: %+v", pr)
	}
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}

	// With nothing to ask about, gh does not run.
	a.FocusThread(plain)
	a.readWorkspace()
	calls := func() int {
		b, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "call")
	}
	// Let any read that was already under way finish before counting.
	time.Sleep(200 * time.Millisecond)
	before := calls()
	a.readPullRequest()
	if got := calls(); got != before {
		t.Fatal("asked GitHub about a folder outside a repository")
	}
	if pr := findThread(t, a, plain).PullRequest; pr.Found() {
		t.Fatalf("%+v", pr)
	}
}

// A pull request is only ever kept for the branch it belongs to, though gh answers
// for whatever is checked out when it runs.
func TestPullRequestKeptForItsOwnBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	scratch := t.TempDir()
	testutil.FakeGH(t, prScript(filepath.Join(scratch, "log"), filepath.Join(scratch, "fail")))
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	dir := findThread(t, a, id).Cwd
	gitIn(t, dir, "init", "-q", "-b", "other")
	a.FocusThread(id)
	awaitBranch(t, a, id, "other", 2*time.Second)

	// A checkout the local reading has not seen yet.
	gitIn(t, dir, "checkout", "-q", "-b", "main")
	a.readPullRequest()
	if pr := findThread(t, a, id).PullRequest; pr.Found() && findThread(t, a, id).Git.Branch == "other" {
		t.Fatalf("branch other shows main's pull request: %+v", pr)
	}
	awaitBranch(t, a, id, "main", workspaceRefresh+2*time.Second)
	awaitPullRequest(t, a, id, 7)

	// A checkout while gh runs: the answer could be for either branch.
	testutil.FakeGH(t, `git checkout -q -b moved
echo '{"number":9,"state":"OPEN","isDraft":false,"title":"Nine"}'`)
	gitIn(t, dir, "checkout", "-q", "-b", "before")
	a.readPullRequest()
	a.mu.Lock()
	_, kept := a.pullRequests[[2]string{dir, "before"}]
	a.mu.Unlock()
	if kept {
		t.Fatal("kept an answer under the branch the checkout left")
	}
}

// A slow network cannot hold up the local reading, or the app's shutdown.
func TestSlowPullRequestDoesNotBlock(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	testutil.FakeGH(t, "exec sleep 60")
	a, err := Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.Create("unused-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	dir := findThread(t, a, id).Cwd
	gitIn(t, dir, "init", "-q", "-b", "main")
	a.FocusThread(id)
	awaitBranch(t, a, id, "main", 2*time.Second)
	time.Sleep(50 * time.Millisecond) // gh is running by now.
	gitIn(t, dir, "checkout", "-q", "-b", "next")
	a.wakeWorkspace()
	awaitBranch(t, a, id, "next", 2*time.Second)
	start := time.Now()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("closing waited %v for gh", took)
	}
}

func TestFocusOnRemovedThreadReadsNothing(t *testing.T) {
	a := openTest(t, t.TempDir())
	a.FocusThread("no-such-thread")
	a.FocusThread("")
}
