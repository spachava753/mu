package desktop_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// worktreeRepo makes a clone with an origin, some branches and a project on it.
func worktreeRepo(t *testing.T, a *app.App) (projectID, repo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(base, "origin.git")
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", origin)
	repo = filepath.Join(base, "acme")
	gitIn(t, base, "clone", "-q", origin, repo)
	gitIn(t, repo, "checkout", "-q", "-b", "main")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "one")
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")
	gitIn(t, repo, "branch", "existing-feature")
	gitIn(t, repo, "push", "-q", "origin", "main:refs/heads/from-colleague")
	gitIn(t, repo, "fetch", "-q")
	projectID, err = a.SaveProject(state.Project{Name: "Acme", Dir: repo})
	if err != nil {
		t.Fatal(err)
	}
	return projectID, repo
}

// settle waits for a condition on the app, redrawing, and lets motion finish.
func settle(t *testing.T, a *app.App, tt *ui.Tester, condition func(app.Snapshot) bool) {
	t.Helper()
	waitView(t, a, tt, condition)
	tt.Frame()
	tt.Advance(320 * time.Millisecond)
	tt.Frame()
}

func awaitText(t *testing.T, a *app.App, tt *ui.Tester, text string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if tt.HasText(text) {
			tt.Advance(320 * time.Millisecond)
			tt.Frame()
			return
		}
		select {
		case <-a.Changes():
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("%q never appeared: %v", text, tt.Texts())
}

func TestWorktreesUI(t *testing.T) {
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprint(size[0]), func(t *testing.T) { worktreesUI(t, size[0], size[1]) })
	}
}

func worktreesUI(t *testing.T, width, height int) {
	a := testApp(t)
	command := testutil.Agent(t)
	project, repo := worktreeRepo(t, a)
	view := desktop.New(a, command)
	tt := ui.NewTester(view.Draw, width, height)

	// The project's folder is still the default, with the choices in view.
	click(t, tt, "New thread in Acme")
	awaitText(t, a, tt, "existing-feature")
	if !tt.HasText("Works in the project's folder") {
		t.Fatal("project folder not the default", tt.Texts())
	}
	screenshot(t, tt, fmt.Sprintf("worktree-form-%d", width))

	// Typing names a new branch; the preview says what happens before it does.
	click(t, tt, "Search or name a new branch")
	tt.Type("feature/login")
	awaitText(t, a, tt, "new branch feature/login from origin/main")
	screenshot(t, tt, fmt.Sprintf("worktree-new-branch-%d", width))
	if out := gitIn(t, repo, "branch", "--list", "feature/login"); out != "" {
		t.Fatal("the preview made a branch")
	}
	click(t, tt, "Create thread")
	waitView(t, a, tt, func(s app.Snapshot) bool {
		return len(s.Threads) == 1 && s.Threads[0].Worktree.Branch == "feature/login"
	})
	s := a.Snapshot()
	first := s.Threads[0]
	if first.ProjectID != project || filepath.Base(first.Cwd) != "feature-login" || first.Cwd == repo {
		t.Fatalf("%+v", first.Thread)
	}
	if branch := gitIn(t, first.Cwd, "branch", "--show-current"); branch != "feature/login" {
		t.Fatal(branch)
	}
	// It opens and connects, in that folder.
	settle(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected })
	if !tt.HasText("feature/login") {
		t.Fatal("thread doesn't show its branch", tt.Texts())
	}
	screenshot(t, tt, fmt.Sprintf("worktree-thread-%d", width))

	// An automatic name needs no typing at all.
	click(t, tt, "New thread in Acme")
	awaitText(t, a, tt, "New branch (automatic name)")
	click(t, tt, "Work on: New branch (automatic name)")
	awaitText(t, a, tt, "on a new branch mu/")
	click(t, tt, "Create thread")
	waitView(t, a, tt, func(s app.Snapshot) bool {
		return len(s.Threads) == 2 && strings.HasPrefix(s.Threads[0].Worktree.Branch, "mu/")
	})
	settle(t, a, tt, func(s app.Snapshot) bool { return s.Threads[0].Connected })

	// Choosing an existing branch checks it out in a worktree of its own.
	click(t, tt, "New thread in Acme")
	click(t, tt, "Search or name a new branch")
	tt.Type("existing")
	awaitText(t, a, tt, "new branch existing from origin/main")
	click(t, tt, "Work on: existing-feature")
	awaitText(t, a, tt, "on the local branch existing-feature")
	screenshot(t, tt, fmt.Sprintf("worktree-existing-branch-%d", width))
	click(t, tt, "Create thread")
	waitView(t, a, tt, func(s app.Snapshot) bool {
		return len(s.Threads) == 3 && s.Threads[0].Worktree.Branch == "existing-feature"
	})

	// A remote branch is fetched and tracked.
	click(t, tt, "New thread in Acme")
	click(t, tt, "Search or name a new branch")
	tt.Type("from-colleague")
	awaitText(t, a, tt, "Fetches origin/from-colleague")
	click(t, tt, "Create thread")
	waitView(t, a, tt, func(s app.Snapshot) bool {
		return len(s.Threads) == 4 && s.Threads[0].Worktree.Branch == "from-colleague"
	})

	// A branch that's already checked out somewhere reuses that checkout.
	click(t, tt, "New thread in Acme")
	click(t, tt, "Search or name a new branch")
	tt.Type("feature/login")
	awaitText(t, a, tt, "Uses the existing worktree")
	screenshot(t, tt, fmt.Sprintf("worktree-attach-%d", width))
	click(t, tt, "Create thread")
	waitView(t, a, tt, func(s app.Snapshot) bool { return len(s.Threads) == 5 && s.Threads[0].Cwd == first.Cwd })

	// An invalid name blocks creation, and says why.
	click(t, tt, "New thread in Acme")
	click(t, tt, "Search or name a new branch")
	tt.Type("not a branch")
	awaitText(t, a, tt, "isn't a valid branch name")
	screenshot(t, tt, fmt.Sprintf("worktree-invalid-%d", width))
	click(t, tt, "Create thread")
	tt.Frame()
	if n := len(a.Snapshot().Threads); n != 5 {
		t.Fatal("created a thread on an invalid branch", n)
	}

	// The project menu lists them, and can start another thread on one.
	click(t, tt, "Project actions: Acme")
	if err := tt.ChooseMenuItem("Worktrees…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(320 * time.Millisecond)
	awaitText(t, a, tt, "Checkouts of this project's repository")
	if !tt.HasText("Project folder") || !tt.HasText("existing-feature") {
		t.Fatal("worktrees not listed", tt.Texts())
	}
	screenshot(t, tt, fmt.Sprintf("worktree-list-%d", width))

	// Deleting one with work in it is refused in the dialog, and costs nothing.
	work := filepath.Join(first.Cwd, "unsaved.txt")
	if err := os.WriteFile(work, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "Delete worktree feature/login")
	tt.Frame()
	tt.Advance(320 * time.Millisecond)
	tt.Frame()
	screenshot(t, tt, fmt.Sprintf("worktree-delete-%d", width))
	click(t, tt, "Delete worktree")
	awaitText(t, a, tt, "uncommitted changes")
	screenshot(t, tt, fmt.Sprintf("worktree-delete-refused-%d", width))
	if _, err := os.Stat(work); err != nil {
		t.Fatal("lost work", err)
	}
	for _, thread := range a.Snapshot().Threads {
		if thread.Archived {
			t.Fatal("a refused deletion archived a thread")
		}
	}
	// With the work put away, it goes, its threads are archived, and the branch
	// can go with it.
	if err := os.Remove(work); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "Also delete the branch feature/login, if it is fully merged")
	click(t, tt, "Delete worktree")
	awaitText(t, a, tt, "Deleted the worktree feature/login")
	screenshot(t, tt, fmt.Sprintf("worktree-deleted-%d", width))
	if _, err := os.Stat(first.Cwd); !os.IsNotExist(err) {
		t.Fatal("folder still there", err)
	}
	if out := gitIn(t, repo, "branch", "--list", "feature/login"); out != "" {
		t.Fatal("branch stayed", out)
	}
	archived := 0
	for _, thread := range a.Snapshot().Threads {
		if thread.Cwd == first.Cwd {
			if !thread.Archived {
				t.Fatal("a thread in the deleted worktree wasn't archived")
			}
			archived++
		}
	}
	if archived != 2 {
		t.Fatal(archived)
	}
	click(t, tt, "Close")
	tt.Advance(320 * time.Millisecond)
	tt.Frame()
	screenshot(t, tt, fmt.Sprintf("worktree-sidebar-%d", width))

	// Clearing the search box goes back to where typing began.
	click(t, tt, "New thread in Acme")
	awaitText(t, a, tt, "Works in the project's folder")
	click(t, tt, "Search or name a new branch")
	tt.Type("x")
	awaitText(t, a, tt, "on a new branch x")
	tt.Key(0, ui.KeyBackspace)
	awaitText(t, a, tt, "Works in the project's folder")
	// A remote's name for a branch means that branch, not a new one named after it.
	tt.Type("origin/main")
	awaitText(t, a, tt, "Works in the project's folder")
	if rect, ok := tt.Find("Work on: Project folder"); !ok || rect.W == 0 {
		t.Fatal("the choice in effect isn't in the list", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()

	// A thread's own menu deletes its worktree too.
	var other app.ThreadView
	for _, thread := range a.Snapshot().Threads {
		if thread.Worktree.Branch == "existing-feature" {
			other = thread
		}
	}
	if other.ID == "" {
		t.Fatal("thread not found")
	}
	if err := a.Rename(other.ID, "Keep branch"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	click(t, tt, "Thread: Keep branch")
	settle(t, a, tt, func(s app.Snapshot) bool {
		for _, thread := range s.Threads {
			if thread.ID == other.ID {
				return thread.Connected
			}
		}
		return false
	})
	click(t, tt, "More thread actions")
	if err := tt.ChooseMenuItem("Delete worktree…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(320 * time.Millisecond)
	tt.Frame()
	click(t, tt, "Delete worktree")
	awaitText(t, a, tt, "No thread selected")
	if _, err := os.Stat(other.Cwd); !os.IsNotExist(err) {
		t.Fatal("folder still there", err)
	}
	if out := gitIn(t, repo, "branch", "--list", "existing-feature"); out == "" {
		t.Fatal("deleted a branch without being asked")
	}
}

func TestWorktreeChoiceNeedsARepository(t *testing.T) {
	a := testApp(t)
	project, err := a.SaveProject(state.Project{Name: "Plain", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	view := desktop.New(a, "unused-agent")
	tt := ui.NewTester(view.Draw, 1100, 780)
	click(t, tt, "New thread in Plain")
	waitView(t, a, tt, func(s app.Snapshot) bool { return s.Worktrees[project] != nil })
	tt.Frame()
	if tt.HasText("Work on") {
		t.Fatal("offered worktrees outside a repository", tt.Texts())
	}
	click(t, tt, "Create thread")
	if s := a.Snapshot(); len(s.Threads) != 1 || s.Threads[0].ProjectID != project {
		t.Fatalf("%+v", s.Threads)
	}
	click(t, tt, "Project actions: Plain")
	if err := tt.ChooseMenuItem("Worktrees…"); err == nil {
		t.Fatal("offered worktrees outside a repository")
	}
}

func TestWorktreeFolderSetting(t *testing.T) {
	for _, size := range [][2]int{{1100, 780}, {800, 600}} {
		t.Run(fmt.Sprint(size[0]), func(t *testing.T) { worktreeFolderSetting(t, size[0], size[1]) })
	}
}

func worktreeFolderSetting(t *testing.T, width, height int) {
	a := testApp(t)
	view := desktop.New(a, "unused-agent")
	tt := ui.NewTester(view.Draw, width, height)
	click(t, tt, "Settings")
	for _, text := range []string{"{repo}", "{repoPath}", "{checkoutParent}", "{branch}", "The folder that checkout is in: ~/dev", "feature/login becomes feature-login"} {
		if !tt.HasText(text) {
			t.Fatalf("%q isn't explained: %v", text, tt.Texts())
		}
	}
	screenshot(t, tt, fmt.Sprintf("worktree-settings-empty-%d", width))
	click(t, tt, "Worktree folder template")
	tt.Type("{nope}/x")
	tt.Frame()
	click(t, tt, "Save folder")
	if !tt.HasText("{nope} isn't a variable") {
		t.Fatal("bad template not reported", tt.Texts())
	}
	click(t, tt, "Worktree folder template")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("~/wt/{repo}/{branch}")
	tt.Frame()
	click(t, tt, "Save folder")
	if a.Snapshot().WorktreeTemplate != "~/wt/{repo}/{branch}" {
		t.Fatal(a.Snapshot().WorktreeTemplate, tt.Texts())
	}
	screenshot(t, tt, fmt.Sprintf("worktree-settings-%d", width))
}
