package desktop

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/workspace"
)

// branchLabel names what the folder has checked out.
func branchLabel(g workspace.Git) string {
	if g.Branch != "" {
		return g.Branch
	}
	return "Detached at " + g.Commit
}

// pullRequestState names a pull request's state for reading.
func pullRequestState(p workspace.PullRequest) string {
	switch p.State {
	case "OPEN":
		if p.Draft {
			return "Draft"
		}
		return "Open"
	case "MERGED":
		return "Merged"
	case "CLOSED":
		return "Closed" // Even a draft, once closed.
	}
	return p.State // Whatever GitHub may add later.
}

// gitLine shows the repository of a thread's folder below the composer, with the
// branch's pull request if it has one. Outside a repository it shows nothing, and
// takes no room.
func gitLine(c *ui.Context, g workspace.Git, pr workspace.PullRequest) {
	if !g.Repo() {
		return
	}
	t := c.Theme()
	color := faint(t)
	item := func(icon *ui.SVG, text, label string) ui.Element {
		return ui.Row(c).Gap(4).Shrink(1).MinWidth(0).Label(label).Tooltip(label).Children(func() {
			ui.Icon(c, icon).Size(12, 12).TextColor(color).Shrink(0)
			ui.Text(c, text).FontSize(12).TextColor(color).SingleLine().Shrink(1).MinWidth(0)
		})
	}
	ui.Row(c.Key("git-line")).Padding(0, 8).Gap(12).Children(func() {
		label := "Git: detached at " + g.Commit
		if g.Branch != "" {
			label = "Git branch: " + g.Branch
		}
		item(branchIcon, branchLabel(g), label)
		if g.Worktree != "" {
			item(folderIcon, filepath.Base(g.Worktree), "Worktree: "+g.Worktree)
		}
		if pr.Found() {
			text := fmt.Sprintf("PR #%d · %s", pr.Number, pullRequestState(pr))
			item(pullRequestIcon, text, text+": "+pr.Title).Shrink(0) // The names can be cut; this says little enough.
		}
	})
}
