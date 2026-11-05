package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spachava753/mu/internal/state"
)

// threadDirLocked chooses a new thread's working directory: its project's
// folder, or a new folder in the general working directory. created reports
// whether Mu made the folder.
func (a *App) threadDirLocked(projectID string) (dir string, created bool, err error) {
	if projectID == "" {
		dir, err = newThreadDir(a.workingDirectoryLocked())
		return dir, err == nil, err
	}
	index := slices.IndexFunc(a.data.Projects, func(p state.Project) bool { return p.ID == projectID })
	if index < 0 {
		return "", false, errors.New("project not found")
	}
	dir = a.data.Projects[index].Dir
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false, errors.New("the project's folder no longer exists: " + dir)
	}
	return dir, false, nil
}

// SuggestProjectName names a project after its folder.
func SuggestProjectName(dir string) string {
	name := filepath.Base(filepath.Clean(dir))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	return name
}

// SaveProject adds or edits a project. Its folder must already exist; Mu
// never creates or deletes project folders. Existing threads keep the folder
// they were created in.
func (a *App) SaveProject(project state.Project) (string, error) {
	project.Name, project.Dir = strings.TrimSpace(project.Name), strings.TrimSpace(project.Dir)
	if project.Name == "" {
		return "", errors.New("enter a project name")
	}
	if !filepath.IsAbs(project.Dir) {
		return "", errors.New("enter an absolute path for the project folder")
	}
	project.Dir = filepath.Clean(project.Dir)
	if info, err := os.Stat(project.Dir); err != nil || !info.IsDir() {
		return "", errors.New("the project folder must be an existing folder")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errAppClosing
	}
	if err := a.checkFolderLocked(project.Dir); err != nil {
		return "", err
	}
	index := -1
	for i, existing := range a.data.Projects {
		if existing.ID == project.ID {
			index = i
		} else if strings.EqualFold(existing.Name, project.Name) {
			return "", errors.New("a project with that name already exists")
		}
	}
	if project.ID != "" && index < 0 {
		return "", errors.New("project not found")
	}
	old := slices.Clone(a.data.Projects)
	if index < 0 {
		project.ID = state.ID()
		a.data.Projects = append(a.data.Projects, project)
	} else {
		a.data.Projects[index] = project
	}
	if err := a.saveLocked(); err != nil {
		a.data.Projects = old
		return "", err
	}
	a.refreshWorktreesLocked(project.ID, false)
	return project.ID, nil
}

// RemoveProject removes only Mu's project entry. Its threads, their sessions
// and the folder are unchanged; the threads are no longer grouped under it.
func (a *App) RemoveProject(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errAppClosing
	}
	index := slices.IndexFunc(a.data.Projects, func(p state.Project) bool { return p.ID == id })
	if index < 0 {
		return errors.New("project not found")
	}
	oldProjects, oldThreads := a.data.Projects, slices.Clone(a.data.Threads)
	a.data.Projects = slices.Delete(slices.Clone(a.data.Projects), index, index+1)
	for i := range a.data.Threads {
		if a.data.Threads[i].ProjectID == id {
			a.data.Threads[i].ProjectID = ""
		}
	}
	if err := a.saveLocked(); err != nil {
		a.data.Projects, a.data.Threads = oldProjects, oldThreads
		return err
	}
	a.setWorktreesLocked(id, nil)
	return nil
}

// projectForDirLocked finds the project whose folder is dir, or whose repository
// has it as a worktree, as last listed. A project's own folder comes first.
func (a *App) projectForDirLocked(dir string) string {
	dir = filepath.Clean(dir)
	for _, p := range a.data.Projects {
		if p.Dir == dir {
			return p.ID
		}
	}
	for _, p := range a.data.Projects {
		if a.worktrees[p.ID].lookup(dir).Path != "" {
			return p.ID
		}
	}
	return ""
}
