package app

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestFocusOnRemovedThreadReadsNothing(t *testing.T) {
	a := openTest(t, t.TempDir())
	a.FocusThread("no-such-thread")
	a.FocusThread("")
}
