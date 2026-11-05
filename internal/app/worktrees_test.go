package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
	"github.com/spachava753/mu/internal/worktree"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// worktreeProject makes a project on a real repository with one commit.
func worktreeProject(t *testing.T, a *App) (projectID, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "one")
	projectID, err = a.SaveProject(state.Project{Name: "Repo", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	awaitWorktrees(t, a, projectID, func(p *ProjectWorktrees) bool { return p.Repo() })
	return projectID, dir
}

func awaitWorktrees(t *testing.T, a *App, projectID string, condition func(*ProjectWorktrees) bool) *ProjectWorktrees {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p := a.Snapshot().Worktrees[projectID]; p != nil && condition(p) {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("worktrees not as expected: %+v", a.Snapshot().Worktrees[projectID])
	return nil
}

func awaitOperation(t *testing.T, a *App, id string) WorktreeOperation {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, op := range a.Snapshot().WorktreeOperations {
			if op.ID == id && op.Done {
				a.ForgetWorktreeOperation(id)
				return op
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return WorktreeOperation{}
}

func newBranchThread(t *testing.T, a *App, command, projectID, branch string) ThreadView {
	t.Helper()
	id, err := a.CreateInWorktree("", command, projectID, worktree.Target{Kind: worktree.KindNew, Branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if op.Error != "" {
		t.Fatal(op.Error)
	}
	return findThread(t, a, op.ThreadID)
}

func TestThreadsInWorktreesAreIsolated(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, dir := worktreeProject(t, a)
	first := newBranchThread(t, a, command, project, "first")
	second := newBranchThread(t, a, command, project, "second")
	want := filepath.Join(a.Snapshot().WorkingDirectory, "worktrees", filepath.Base(dir), "first")
	if first.Cwd != want || first.ProjectID != project || first.Cwd == second.Cwd {
		t.Fatalf("%+v", first.Thread)
	}
	if branch := gitOut(t, first.Cwd, "branch", "--show-current"); branch != "first" {
		t.Fatal(branch)
	}
	// Both run at once, on one shared agent process, in their own folders.
	var wg sync.WaitGroup
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.Send(id, "hello"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, id := range []string{first.ID, second.ID} {
		v := await(t, a, id, func(v ThreadView) bool { return !v.Running && v.Connected })
		if v.Error != "" || lastText(v) != "Echo: hello" {
			t.Fatalf("%+v", v)
		}
	}
	// Each names its worktree, once listed; the project's own folder names none.
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 3 })
	if v := findThread(t, a, first.ID); v.Worktree.Branch != "first" || v.Worktree.Main {
		t.Fatalf("%+v", v.Worktree)
	}
	shared, err := a.Create(command, project)
	if err != nil {
		t.Fatal(err)
	}
	if v := findThread(t, a, shared); v.Cwd != dir || v.Worktree.Path != "" {
		t.Fatalf("%+v", v)
	}
	// Attaching shares the worktree's folder and changes no Git state.
	head := gitOut(t, first.Cwd, "rev-parse", "HEAD")
	id, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindPath, Path: first.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	attached := awaitOperation(t, a, id)
	if v := findThread(t, a, attached.ThreadID); attached.Error != "" || v.Cwd != first.Cwd || v.Worktree.Branch != "first" {
		t.Fatalf("%+v %+v", attached, v)
	}
	if gitOut(t, first.Cwd, "rev-parse", "HEAD") != head {
		t.Fatal("attaching moved the worktree")
	}
}

func TestParallelCreationsDontCollide(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	// Different branches all succeed; the same one twice succeeds once.
	branches := []string{"a", "b", "c", "d", "e", "same", "same"}
	ids := make([]string, len(branches))
	var wg sync.WaitGroup
	for i, branch := range branches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindNew, Branch: branch})
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		}()
	}
	wg.Wait()
	failures := 0
	dirs := map[string]bool{}
	for _, id := range ids {
		op := awaitOperation(t, a, id)
		if op.Error != "" {
			failures++
			if !strings.Contains(op.Error, "already exists") {
				t.Error(op.Error)
			}
			continue
		}
		dirs[findThread(t, a, op.ThreadID).Cwd] = true
	}
	if failures != 1 || len(dirs) != 6 {
		t.Fatalf("%d failures, folders: %v", failures, dirs)
	}
}

func TestDeleteWorktreeClosesSessionsAndArchives(t *testing.T) {
	command := testutil.Agent(t)
	dir := t.TempDir()
	a := openTest(t, dir)
	project, repo := worktreeProject(t, a)
	v := newBranchThread(t, a, command, project, "done")
	v = sendDone(t, a, v.ID, "hello")
	other := newBranchThread(t, a, command, project, "other")
	sendDone(t, a, other.ID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 3 })

	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if op.Error != "" || !slices.Equal(op.Archived, []string{v.ID}) {
		t.Fatalf("%+v", op)
	}
	if _, err := os.Stat(v.Cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("folder still there:", err)
	}
	after := findThread(t, a, v.ID)
	if !after.Archived || after.Connected || after.Configuring || after.DeletingWorktree || after.SessionID != v.SessionID {
		t.Fatalf("%+v", after)
	}
	if gitOut(t, repo, "branch", "--list", "done") == "" {
		t.Fatal("branch deleted without being asked")
	}
	// Other worktrees keep going.
	if w := sendDone(t, a, other.ID, "again"); w.Error != "" || !w.Connected {
		t.Fatalf("%+v", w)
	}
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	if _, err := a.DeleteWorktree(project, v.Cwd, false); err == nil {
		t.Fatal("deleted a worktree twice")
	}
	// The archive is saved.
	data, err := os.ReadFile(filepath.Join(dir, "threads.json"))
	if err != nil || !strings.Contains(string(data), `"archived": true`) {
		t.Fatal(string(data), err)
	}
}

func TestDeleteWorktreeWaitsForTheCloseBarrier(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, testutil.Agent(t)+" slow-close", project, "x")
	sendDone(t, a, v.ID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	started := time.Now()
	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	// Meanwhile the thread can't be used, and says why.
	await(t, a, v.ID, func(v ThreadView) bool { return v.DeletingWorktree && v.Configuring })
	if err := a.Send(v.ID, "hello"); err == nil {
		t.Fatal("sent to a thread whose worktree is being deleted")
	}
	if op := awaitOperation(t, a, id); op.Error != "" {
		t.Fatal(op.Error)
	}
	if time.Since(started) < 120*time.Millisecond {
		t.Fatal("removed the worktree before its session closed")
	}
}

func TestDeleteWorktreeRefusalLeavesThreadsWorking(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, repo := worktreeProject(t, a)
	v := newBranchThread(t, a, command, project, "dirty")
	v = sendDone(t, a, v.ID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	work := filepath.Join(v.Cwd, "work.txt")
	if err := os.WriteFile(work, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := a.DeleteWorktree(project, v.Cwd, true)
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if !strings.Contains(op.Error, "uncommitted") {
		t.Fatalf("%+v", op)
	}
	after := findThread(t, a, v.ID)
	if after.Archived || !after.Connected || after.Configuring || after.DeletingWorktree {
		t.Fatalf("session disturbed by a refused deletion: %+v", after)
	}
	if data, err := os.ReadFile(work); err != nil || string(data) != "work" {
		t.Fatal("lost work")
	}
	if gitOut(t, repo, "branch", "--list", "dirty") == "" {
		t.Fatal("deleted the branch after a refusal")
	}
	if w := sendDone(t, a, v.ID, "again"); w.Error != "" {
		t.Fatal(w.Error)
	}
	// A running turn also stops it, before anything starts.
	if err := a.Send(v.ID, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, v.ID, func(v ThreadView) bool { return v.Running })
	if _, err := a.DeleteWorktree(project, v.Cwd, false); err == nil || !strings.Contains(err.Error(), "at work") {
		t.Fatal(err)
	}
	a.Stop(v.ID)
	await(t, a, v.ID, func(v ThreadView) bool { return !v.Running })
}

func TestDeleteWorktreeBranch(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, repo := worktreeProject(t, a)
	merged := newBranchThread(t, a, command, project, "merged")
	unmerged := newBranchThread(t, a, command, project, "unmerged")
	gitIn(t, unmerged.Cwd, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "work")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 3 })
	id, err := a.DeleteWorktree(project, merged.Cwd, true)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); op.Error != "" || !slices.Contains(op.Notes, "Deleted branch merged.") {
		t.Fatalf("%+v", op)
	}
	if gitOut(t, repo, "branch", "--list", "merged") != "" {
		t.Fatal("branch stayed")
	}
	id, err = a.DeleteWorktree(project, unmerged.Cwd, true)
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if op.Error != "" || len(op.Notes) != 1 || !strings.Contains(op.Notes[0], "Kept branch unmerged") {
		t.Fatalf("%+v", op)
	}
	if gitOut(t, repo, "branch", "--list", "unmerged") == "" {
		t.Fatal("unmerged branch lost")
	}
}

func TestDeleteWorktreeProtectsProjects(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, repo := worktreeProject(t, a)
	if _, err := a.DeleteWorktree(project, repo, false); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatal(err)
	}
	if _, err := a.DeleteWorktree(project, filepath.Join(repo, "nope"), false); err == nil {
		t.Fatal("deleted something that isn't listed")
	}
	v := newBranchThread(t, a, command, project, "x")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	// Another project in that folder keeps it; a project in a linked worktree
	// can't delete its own folder.
	linked, err := a.SaveProject(state.Project{Name: "Linked", Dir: v.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	awaitWorktrees(t, a, linked, func(p *ProjectWorktrees) bool { return p.Repo() })
	if _, err := a.DeleteWorktree(project, v.Cwd, false); err == nil || !strings.Contains(err.Error(), "project Linked") {
		t.Fatal(err)
	}
	if _, err := a.DeleteWorktree(linked, v.Cwd, false); err == nil {
		t.Fatal("deleted a project's own folder")
	}
	if _, err := a.DeleteWorktree(linked, repo, false); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatal(err)
	}
	if _, err := os.Stat(v.Cwd); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedFolderIsExplainedOnConnect(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, "/no/such/agent", project, "x")
	if err := os.RemoveAll(v.Cwd); err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(v.ID); err != nil {
		t.Fatal(err)
	}
	after := await(t, a, v.ID, func(v ThreadView) bool { return !v.Configuring && v.SessionError != "" })
	if !strings.HasPrefix(after.SessionError, "This thread's folder no longer exists: "+v.Cwd) || strings.Contains(after.SessionError, "/no/such/agent") {
		t.Fatal(after.SessionError)
	}
}

func TestImportedWorktreeSessionJoinsItsProject(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := worktreeProject(t, a)
	id, err := a.CreateInWorktree(agentID, "", project, worktree.Target{Kind: worktree.KindNew, Branch: "x"})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if op.Error != "" {
		t.Fatal(op.Error)
	}
	v := sendDone(t, a, op.ThreadID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	if err := a.Delete(v.ID); err != nil {
		t.Fatal(err)
	}
	c := discoverDone(t, a, agentID)
	ids, err := a.Import(c.Generation, []string{v.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if imported := findThread(t, a, ids[0]); imported.ProjectID != project || imported.Worktree.Branch != "x" {
		t.Fatalf("%+v", imported)
	}
}

func TestWorktreeTemplateAndPreview(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, repo := worktreeProject(t, a)
	for _, bad := range []string{"{nope}/x", "/x/"} {
		if err := a.SetWorktreeTemplate(bad); err == nil {
			t.Fatal("accepted template", bad)
		}
	}
	target := worktree.Target{Kind: worktree.KindNew, Branch: "feature/y"}
	a.PreviewWorktree(project, target)
	preview := func(check func(WorktreePreview) bool) WorktreePreview {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if p := a.Snapshot().WorktreePreview; p.Done && check(p) {
				return p
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%+v", a.Snapshot().WorktreePreview)
		return WorktreePreview{}
	}
	want := filepath.Join(a.Snapshot().WorkingDirectory, "worktrees", filepath.Base(repo), "feature-y")
	preview(func(p WorktreePreview) bool { return p.Plan.Path == want })
	// A change of template updates the preview.
	if err := a.SetWorktreeTemplate("{repoPath}/.worktrees/{branch}"); err != nil {
		t.Fatal(err)
	}
	preview(func(p WorktreePreview) bool { return p.Plan.Path == filepath.Join(repo, ".worktrees", "feature-y") })
	// Previews write nothing.
	if entries, _ := os.ReadDir(filepath.Join(repo, ".worktrees")); len(entries) != 0 {
		t.Fatal(entries)
	}
	if gitOut(t, repo, "branch", "--list", "feature/y") != "" {
		t.Fatal("preview made a branch")
	}
	// A problem is an answer too, and a new target replaces it.
	a.PreviewWorktree(project, worktree.Target{Kind: worktree.KindNew, Branch: "main"})
	if p := preview(func(p WorktreePreview) bool { return p.Error != "" }); !strings.Contains(p.Error, "already exists") {
		t.Fatal(p.Error)
	}
	a.ClearWorktreePreview()
	if p := a.Snapshot().WorktreePreview; p.Target != (worktree.Target{}) {
		t.Fatalf("%+v", p)
	}
	if err := a.SetWorktreeTemplate(""); err != nil {
		t.Fatal(err)
	}
	if s := a.Snapshot(); s.WorktreeTemplate != "" || !strings.HasSuffix(s.DefaultWorktreeTemplate, filepath.Join("worktrees", "{repo}", "{branch}")) {
		t.Fatalf("%+v", s.DefaultWorktreeTemplate)
	}
}

func TestCreationFailureIsReported(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	id, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindNew, Branch: "a b"})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if !strings.Contains(op.Error, "valid branch") || op.ThreadID != "" {
		t.Fatalf("%+v", op)
	}
	if n := len(a.Snapshot().Threads); n != 0 {
		t.Fatal(n)
	}
	if _, err := a.CreateInWorktree("missing-agent", "", project, worktree.Target{Kind: worktree.KindNew, Branch: "x"}); err == nil {
		t.Fatal("accepted an agent that doesn't exist")
	}
	if _, err := a.CreateInWorktree("", command, "missing-project", worktree.Target{Kind: worktree.KindNew, Branch: "x"}); err == nil {
		t.Fatal("accepted a project that doesn't exist")
	}
}

func TestNonRepositoryProjectHasNoWorktrees(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, err := a.SaveProject(state.Project{Name: "Plain", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if p := awaitWorktrees(t, a, project, func(*ProjectWorktrees) bool { return true }); p.Repo() {
		t.Fatalf("%+v", p)
	}
	if err := a.RemoveProject(project); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Snapshot().Worktrees[project]; ok {
		t.Fatal("kept the reading of a removed project")
	}
}

func TestFocusFollowsWorktreesMadeOutsideMu(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, repo := worktreeProject(t, a)
	id, err := a.Create(command, project)
	if err != nil {
		t.Fatal(err)
	}
	a.FocusThread(id)
	outside := filepath.Join(t.TempDir(), "outside")
	gitIn(t, repo, "worktree", "add", "-q", "-b", "outside", outside)
	if p := awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 }); p.Worktrees[1].Branch != "outside" {
		t.Fatal(fmt.Sprintf("%+v", p.Worktrees))
	}
}

func TestStartupReadsProjects(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, _ := worktreeProject(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b := openTest(t, dir)
	awaitWorktrees(t, b, project, func(p *ProjectWorktrees) bool { return p.Repo() })
}

func TestDeleteWorktreeRefusedCloseKeepsTheFolder(t *testing.T) {
	// A saved agent's process goes on running after a thread lets go of it.
	a, err := Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err == nil || !strings.Contains(err.Error(), "close session") {
			t.Error("close failure hidden:", err)
		}
	})
	config, err := a.SaveAgent(state.AgentConfig{Name: "Stubborn", Command: testutil.Agent(t) + " close-error"}, true)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := worktreeProject(t, a)
	created, err := a.CreateInWorktree(config, "", project, worktree.Target{Kind: worktree.KindNew, Branch: "x"})
	if err != nil {
		t.Fatal(err)
	}
	made := awaitOperation(t, a, created)
	if made.Error != "" {
		t.Fatal(made.Error)
	}
	v := sendDone(t, a, made.ThreadID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); !strings.Contains(op.Error, "wouldn't close") {
		t.Fatalf("%+v", op)
	}
	if _, err := os.Stat(v.Cwd); err != nil {
		t.Fatal("deleted a folder the agent may still be using:", err)
	}
	if after := findThread(t, a, v.ID); after.Archived || after.Configuring || after.DeletingWorktree {
		t.Fatalf("%+v", after)
	}
	// Trying again, with nothing left to close, doesn't get around the refusal.
	id, err = a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); !strings.Contains(op.Error, "wouldn't close") {
		t.Fatalf("%+v", op)
	}
	if _, err := os.Stat(v.Cwd); err != nil {
		t.Fatal("a second try deleted the folder:", err)
	}
	// Nor does taking the thread out of Mu: the agent is still there.
	if err := a.Delete(v.ID); err != nil {
		t.Fatal(err)
	}
	id, err = a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); !strings.Contains(op.Error, "wouldn't close") {
		t.Fatalf("%+v", op)
	}
}

func TestALaterCloseThatSucceedsClearsARefusal(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, testutil.Agent(t), project, "x")
	sendDone(t, a, v.ID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	a.mu.Lock()
	r := a.runtimes[v.ID]
	refused := a.trackFolderCloseLocked(r.conn, r.session, v.Cwd, make(chan struct{}))
	refused.refusal = "refused"
	close(refused.done)
	a.mu.Unlock()
	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); op.Error != "" {
		t.Fatalf("%+v", op)
	}
}

func findThreadLocked(a *App, id string) state.Thread {
	return *a.threadLocked(id).Thread
}

// openRefusing opens an app whose agents refuse to close sessions, which it reports
// when it closes.
func openRefusing(t *testing.T) *App {
	t.Helper()
	a, err := Open(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err == nil || !strings.Contains(err.Error(), "close session") {
			t.Error("close failure hidden:", err)
		}
	})
	return a
}

func TestARefusedForkCloseCounts(t *testing.T) {
	a := openRefusing(t)
	config, err := a.SaveAgent(state.AgentConfig{Name: "Stubborn", Command: testutil.Agent(t) + " close-error"}, true)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := worktreeProject(t, a)
	created, err := a.CreateInWorktree(config, "", project, worktree.Target{Kind: worktree.KindNew, Branch: "x"})
	if err != nil {
		t.Fatal(err)
	}
	made := awaitOperation(t, a, created)
	if made.Error != "" {
		t.Fatal(made.Error)
	}
	v := sendDone(t, a, made.ThreadID, "hello")
	fork, err := a.Fork(v.ID)
	if fork == "" || err == nil {
		t.Fatalf("%q %v", fork, err)
	}
	a.mu.Lock()
	closes := a.folderClosesLocked(realPath(v.Cwd))
	refused := len(closes) == 1 && closes[0].refusal != "" && closes[0].session == findThreadLocked(a, fork).SessionID
	a.mu.Unlock()
	if !refused {
		t.Fatalf("%+v", closes)
	}
}

func TestARequestMuStoppedWaitingForHoldsTheFolder(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, testutil.Agent(t)+" slow-new", project, "x")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	a.mu.Lock()
	a.loadTimeout = 50 * time.Millisecond
	a.mu.Unlock()
	if err := a.Connect(v.ID); err != nil {
		t.Fatal(err)
	}
	await(t, a, v.ID, func(v ThreadView) bool { return !v.Configuring && strings.Contains(v.SessionError, "did not respond") })
	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); !strings.Contains(op.Error, "hasn't answered") {
		t.Fatalf("%+v", op)
	}
	if _, err := os.Stat(v.Cwd); err != nil {
		t.Fatal(err)
	}
	// Once it has settled, and any late session has closed, the folder is free.
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.Lock()
		remaining := len(a.folderClosesLocked(realPath(v.Cwd)))
		a.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the folder stayed held after the request settled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	id, err = a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); op.Error != "" {
		t.Fatalf("%+v", op)
	}
}

func TestDeletionWaitsForACloseWhoseThreadIsGone(t *testing.T) {
	a := openRefusing(t)
	config, err := a.SaveAgent(state.AgentConfig{Name: "Slow", Command: testutil.Agent(t) + " slower-close close-error"}, true)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := worktreeProject(t, a)
	created, err := a.CreateInWorktree(config, "", project, worktree.Target{Kind: worktree.KindNew, Branch: "x"})
	if err != nil {
		t.Fatal(err)
	}
	made := awaitOperation(t, a, created)
	if made.Error != "" {
		t.Fatal(made.Error)
	}
	v := sendDone(t, a, made.ThreadID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	if err := a.Delete(v.ID); err != nil {
		t.Fatal(err)
	}
	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); !strings.Contains(op.Error, "wouldn't close") {
		t.Fatalf("%+v", op)
	}
	if _, err := os.Stat(v.Cwd); err != nil {
		t.Fatal("deleted the folder before the close was answered:", err)
	}
}

func TestRefusalLastsUntilARetiredProcessHasExited(t *testing.T) {
	a := openRefusing(t)
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, testutil.Agent(t)+" close-error ignore-eof", project, "x")
	sendDone(t, a, v.ID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); !strings.Contains(op.Error, "wouldn't close") {
		t.Fatalf("%+v", op)
	}
	if _, err := os.Stat(v.Cwd); err != nil {
		t.Fatal("deleted the folder while the process was still running:", err)
	}
	// Once it has exited, nothing is using the folder.
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.Lock()
		remaining := len(a.folderClosesLocked(realPath(v.Cwd)))
		a.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refusal outlived the process")
		}
		time.Sleep(20 * time.Millisecond)
	}
	id, err = a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); op.Error != "" {
		t.Fatalf("%+v", op)
	}
}

func TestCreationAsksForTheRemoteDefaultItself(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(base, "origin.git")
	gitIn(t, base, "init", "-q", "--bare", "-b", "main", origin)
	repo := filepath.Join(base, "repo")
	gitIn(t, base, "clone", "-q", origin, repo)
	gitIn(t, repo, "checkout", "-q", "-b", "main")
	gitIn(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "one")
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")
	gitIn(t, repo, "remote", "set-head", "origin", "main")
	project, err := a.SaveProject(state.Project{Name: "Repo", Dir: repo})
	if err != nil {
		t.Fatal(err)
	}
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return p.DefaultBase == "origin/main" })
	// The default moves, and nothing has read the remote since.
	gitIn(t, repo, "push", "-q", "origin", "main:refs/heads/develop")
	gitIn(t, origin, "symbolic-ref", "HEAD", "refs/heads/develop")
	id, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindNew, Branch: "x"})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if op.Error != "" || len(op.Notes) != 1 || !strings.Contains(op.Notes[0], "origin/develop") {
		t.Fatalf("%+v", op)
	}
}

func TestBranchResolvedBeforeADeletionDoesntAttachToNothing(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, command+" slow-close", project, "x")
	sendDone(t, a, v.ID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	remove, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	await(t, a, v.ID, func(v ThreadView) bool { return v.DeletingWorktree })
	id, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindBranch, Branch: "x"})
	if err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, a, id)
	if removed := awaitOperation(t, a, remove); removed.Error != "" {
		t.Fatalf("%+v", removed)
	}
	if op.ThreadID != "" {
		if thread := findThread(t, a, op.ThreadID); thread.Archived {
			return // It got in ahead of the deletion, which took it along.
		} else if _, err := os.Stat(thread.Cwd); err != nil {
			t.Fatalf("a thread in a folder that's gone: %+v", thread.Thread)
		}
	}
}

func TestAFolderBeingDeletedCantTakeNewUsers(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, command+" slow-close", project, "x")
	sendDone(t, a, v.ID, "hello")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	id, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	await(t, a, v.ID, func(v ThreadView) bool { return v.DeletingWorktree })
	if _, err := a.SaveProject(state.Project{Name: "Late", Dir: v.Cwd}); err == nil || !strings.Contains(err.Error(), "being deleted") {
		t.Fatal(err)
	}
	a.mu.Lock()
	_, err = a.addThreadLocked(command, "", "", project, v.Cwd)
	a.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "being deleted") {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, id); op.Error != "" {
		t.Fatal(op.Error)
	}
	// The reservation ends with the operation.
	if err := os.MkdirAll(v.Cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveProject(state.Project{Name: "Later", Dir: v.Cwd}); err != nil {
		t.Fatal(err)
	}
}

func TestStalledRemoteHoldsUpOnlyItsOwnOperation(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, repo := worktreeProject(t, a)
	v := newBranchThread(t, a, command, project, "attachable")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	// A remote whose helper never answers.
	helper := filepath.Join(t.TempDir(), "git-remote-stall")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(helper)+string(os.PathListSeparator)+os.Getenv("PATH"))
	gitIn(t, repo, "remote", "add", "slow", "stall::nowhere")
	gitIn(t, repo, "update-ref", "refs/remotes/slow/waiting", "HEAD")
	stalled, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindBranch, Branch: "waiting", Remote: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	attach, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindPath, Path: v.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	if op := awaitOperation(t, a, attach); op.Error != "" {
		t.Fatal(op.Error)
	}
	if time.Since(started) > 10*time.Second {
		t.Fatal("waited for another operation's remote")
	}
	for _, op := range a.Snapshot().WorktreeOperations {
		if op.ID == stalled && op.Done {
			t.Fatalf("the remote didn't stall: %+v", op)
		}
	}
}

func TestDeleteWorktreeCoversAThreadAttachedAheadOfIt(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, command, project, "x")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	attach, err := a.CreateInWorktree("", command, project, worktree.Target{Kind: worktree.KindPath, Path: v.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	remove, err := a.DeleteWorktree(project, v.Cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	attached := awaitOperation(t, a, attach)
	if op := awaitOperation(t, a, remove); op.Error != "" || !slices.Contains(op.Archived, attached.ThreadID) || !slices.Contains(op.Archived, v.ID) {
		t.Fatalf("%+v %+v", op, attached)
	}
}

func TestDeleteWorktreeSeesThroughSymlinks(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, testutil.Agent(t), project, "x")
	awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(v.Cwd, link); err != nil {
		t.Skip(err)
	}
	other, err := a.SaveProject(state.Project{Name: "Linked", Dir: link})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteWorktree(project, v.Cwd, false); err == nil || !strings.Contains(err.Error(), "project Linked") {
		t.Fatal(err)
	}
	if err := a.RemoveProject(other); err != nil {
		t.Fatal(err)
	}
}

func TestProjectsSharingARepositoryTakeTurns(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	first, repo := worktreeProject(t, a)
	second, err := a.SaveProject(state.Project{Name: "Again", Dir: repo})
	if err != nil {
		t.Fatal(err)
	}
	awaitWorktrees(t, a, second, func(p *ProjectWorktrees) bool { return p.Repo() })
	// The two names want one folder: the later one takes the next free name.
	var ids []string
	for i := range 6 {
		for _, target := range []struct{ project, branch string }{{first, fmt.Sprintf("r%d/x", i)}, {second, fmt.Sprintf("r%d-x", i)}} {
			id, err := a.CreateInWorktree("", command, target.project, worktree.Target{Kind: worktree.KindNew, Branch: target.branch})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if op := awaitOperation(t, a, id); op.Error != "" {
			t.Fatal(op.Error)
		}
	}
}

func TestThreadInASubfolderOfAWorktree(t *testing.T) {
	a := openTest(t, t.TempDir())
	project, _ := worktreeProject(t, a)
	v := newBranchThread(t, a, testutil.Agent(t), project, "x")
	listing := awaitWorktrees(t, a, project, func(p *ProjectWorktrees) bool { return len(p.Worktrees) == 2 })
	if w := listing.lookup(filepath.Join(v.Cwd, "a", "b")); w.Branch != "x" {
		t.Fatalf("%+v", w)
	}
	if w := listing.lookup(filepath.Join(listing.Root, "a", "b")); w.Path != "" {
		t.Fatalf("%+v", w)
	}
	if w := (*ProjectWorktrees)(nil).lookup("/a"); w.Path != "" {
		t.Fatalf("%+v", w)
	}
}
