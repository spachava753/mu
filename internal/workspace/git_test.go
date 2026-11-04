package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// repo makes a repository with one commit on branch main.
func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "one")
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func inspect(t *testing.T, dir string) Git {
	t.Helper()
	g, err := InspectGit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestInspectBranch(t *testing.T) {
	dir := repo(t)
	if g := inspect(t, dir); g != (Git{Branch: "main"}) {
		t.Fatalf("%+v", g)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if g := inspect(t, sub); g != (Git{Branch: "main"}) {
		t.Fatalf("subfolder: %+v", g)
	}
	run(t, dir, "checkout", "-q", "-b", "feature/long-name")
	if g := inspect(t, dir); g.Branch != "feature/long-name" {
		t.Fatalf("%+v", g)
	}
}

func TestInspectDetachedHead(t *testing.T) {
	dir := repo(t)
	run(t, dir, "checkout", "-q", "--detach")
	g := inspect(t, dir)
	if g.Branch != "" || len(g.Commit) < 7 || !g.Repo() {
		t.Fatalf("%+v", g)
	}
}

func TestInspectUnbornBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "first")
	if g := inspect(t, dir); g != (Git{Branch: "first"}) {
		t.Fatalf("%+v", g)
	}
}

func TestInspectWorktree(t *testing.T) {
	dir := repo(t)
	linked := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-linked")
	t.Cleanup(func() { _ = os.RemoveAll(linked) })
	run(t, dir, "worktree", "add", "-q", "-b", "side", linked)
	if g := inspect(t, linked); g != (Git{Branch: "side", Worktree: linked}) {
		t.Fatalf("linked: %+v", g)
	}
	sub := filepath.Join(linked, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if g := inspect(t, sub); g != (Git{Branch: "side", Worktree: linked}) {
		t.Fatalf("linked subfolder: %+v", g)
	}
	if g := inspect(t, dir); g != (Git{Branch: "main"}) {
		t.Fatalf("main: %+v", g)
	}
}

func TestInspectOutsideRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	if g := inspect(t, dir); g.Repo() {
		t.Fatalf("no repository: %+v", g)
	}
	if g := inspect(t, filepath.Join(dir, "gone")); g.Repo() {
		t.Fatalf("missing folder: %+v", g)
	}
}

// A repository git cannot read is an unknown answer, not the absence of one.
func TestInspectUnreadableRepository(t *testing.T) {
	dir := repo(t)
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if g, err := InspectGit(context.Background(), dir); err == nil {
		t.Fatalf("read a broken repository as %+v", g)
	}
}

func TestInspectInsideGitFolder(t *testing.T) {
	dir := repo(t)
	if g := inspect(t, filepath.Join(dir, ".git")); g.Repo() {
		t.Fatalf("no working tree: %+v", g)
	}
}

func TestInspectIgnoresGitEnvironment(t *testing.T) {
	dir := repo(t)
	other := repo(t)
	run(t, other, "checkout", "-q", "-b", "other")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	if g := inspect(t, dir); g.Branch != "main" {
		t.Fatalf("followed GIT_DIR: %+v", g)
	}
}

func TestInspectWithoutGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if g := inspect(t, t.TempDir()); g.Repo() {
		t.Fatalf("%+v", g)
	}
}

func TestInspectEndedContext(t *testing.T) {
	dir := repo(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if _, err := InspectGit(ctx, dir); err == nil {
		t.Fatal("an ended context is an unknown answer, not an empty one")
	}
}
