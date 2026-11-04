package desktop

import (
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/state"
)

// threadGroup is one section of the sidebar's thread list.
type threadGroup struct {
	Key, Name string // Name is empty when the section has no heading
	Project   *state.Project
	Threads   []app.ThreadView
}

// groupThreads arranges the listed threads into the sidebar's sections: by
// project once any project exists, with threads without a project last. Every
// project has a section in the active list, even with no threads yet, since
// that is where its actions are.
func groupThreads(s app.Snapshot, archived bool) []threadGroup {
	byProject := map[string][]app.ThreadView{}
	for _, t := range s.Threads {
		if t.Archived == archived {
			byProject[t.ProjectID] = append(byProject[t.ProjectID], t)
		}
	}
	var groups []threadGroup
	for i, p := range s.Projects {
		if threads := byProject[p.ID]; len(threads) > 0 || !archived {
			groups = append(groups, threadGroup{Key: p.ID, Name: p.Name, Project: &s.Projects[i], Threads: threads})
		}
		delete(byProject, p.ID)
	}
	if threads := byProject[""]; len(threads) > 0 {
		name := "No project"
		if len(s.Projects) == 0 {
			name = ""
		}
		groups = append(groups, threadGroup{Key: "", Name: name, Threads: threads})
		delete(byProject, "")
	}
	// Not expected, since removing a project clears it from its threads.
	for _, t := range s.Threads {
		if threads := byProject[t.ProjectID]; t.Archived == archived && len(threads) > 0 {
			groups = append(groups, threadGroup{Key: t.ProjectID, Threads: threads})
			delete(byProject, t.ProjectID)
		}
	}
	return groups
}

// threadOrder lists thread IDs as the groups show them.
func threadOrder(groups []threadGroup) []string {
	var order []string
	for _, g := range groups {
		for _, t := range g.Threads {
			order = append(order, t.ID)
		}
	}
	return order
}
