package desktop

import "github.com/spachava753/mu/internal/app"

// threadGroup is one section of the sidebar's thread list.
type threadGroup struct {
	Key, Name string // Name is empty when the section has no heading
	Threads   []app.ThreadView
}

// groupThreads arranges the listed threads into the sidebar's sections: by
// project once any project exists, with threads without a project last.
func groupThreads(s app.Snapshot, archived bool) []threadGroup {
	byProject := map[string][]app.ThreadView{}
	for _, t := range s.Threads {
		if t.Archived == archived {
			byProject[t.ProjectID] = append(byProject[t.ProjectID], t)
		}
	}
	var groups []threadGroup
	for _, p := range s.Projects {
		if threads := byProject[p.ID]; len(threads) > 0 {
			groups = append(groups, threadGroup{Key: p.ID, Name: p.Name, Threads: threads})
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
