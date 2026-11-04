package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func TestThreadFoldersAndWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	work := a.Snapshot().WorkingDirectory
	first, second := createTest(t, a, "agent"), createTest(t, a, "agent")
	cwd := findThread(t, a, first).Cwd
	if filepath.Dir(cwd) != work || cwd == findThread(t, a, second).Cwd {
		t.Fatalf("threads did not get their own folders in %s: %s", work, cwd)
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		t.Fatal("thread folder not created", err)
	}
	for _, bad := range []string{"relative/path", filepath.Join(cwd, "file")} {
		if bad != "relative/path" {
			if err := os.WriteFile(bad, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.SetWorkingDirectory(bad); err == nil {
			t.Fatal("accepted working directory", bad)
		}
	}
	moved := filepath.Join(t.TempDir(), "not-yet-created")
	if err := a.SetWorkingDirectory(moved); err != nil {
		t.Fatal(err)
	}
	third := createTest(t, a, "agent")
	if filepath.Dir(findThread(t, a, third).Cwd) != moved || findThread(t, a, first).Cwd != cwd {
		t.Fatal("new working directory not used, or existing thread moved")
	}
	// A failed save leaves no thread and no empty folder behind.
	path := filepath.Join(dir, "threads.json")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Create("agent", ""); err == nil {
		t.Fatal("expected save failure")
	}
	if entries, _ := os.ReadDir(moved); len(entries) != 1 {
		t.Fatalf("failed creation left a folder: %v", entries)
	}
	_ = os.Remove(path)
	_ = os.Rename(path+".saved", path)
}

func TestProjectsShareTheirFolder(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	folder := t.TempDir()
	for _, bad := range []state.Project{{Name: "", Dir: folder}, {Name: "Missing", Dir: filepath.Join(folder, "missing")}, {Name: "Relative", Dir: "relative"}} {
		if _, err := a.SaveProject(bad); err == nil {
			t.Fatal("accepted project", bad)
		}
	}
	project, err := a.SaveProject(state.Project{Name: "Site", Dir: folder + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveProject(state.Project{Name: "site", Dir: folder}); err == nil {
		t.Fatal("duplicate project name accepted")
	}
	first, err := a.Create(command, project)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Create(command, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first, second} {
		if v := findThread(t, a, id); v.Cwd != folder || v.ProjectID != project {
			t.Fatalf("project thread not in project folder: %+v", v.Thread)
		}
	}
	if entries, _ := os.ReadDir(a.Snapshot().WorkingDirectory); len(entries) != 0 {
		t.Fatal("project thread created a folder in the working directory")
	}
	v := sendDone(t, a, first, "hello")
	if v.Error != "" || lastText(v) != "Echo: hello" {
		t.Fatal(v)
	}
	// Imported sessions in a project's folder join that project.
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	c := discoverDone(t, a, agentID)
	ids, err := a.Import(c.Generation, []string{v.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if imported := findThread(t, a, ids[0]); imported.ProjectID != project {
		t.Fatal("imported session not assigned to its project", imported.Thread)
	}
	if err := a.RemoveProject(project); err != nil {
		t.Fatal(err)
	}
	if v := findThread(t, a, first); v.ProjectID != "" || v.Cwd != folder || len(a.Snapshot().Projects) != 0 {
		t.Fatal("project removal changed the thread's folder or kept the project", v.Thread)
	}
	if _, err := a.Create(command, project); err == nil {
		t.Fatal("created a thread in a removed project")
	}
}

func TestSuggestProjectName(t *testing.T) {
	for dir, want := range map[string]string{"/home/me/website": "website", "/home/me/website/": "website", "/": "", "": ""} {
		if got := SuggestProjectName(dir); got != want {
			t.Fatalf("%q: %q", dir, got)
		}
	}
}
