package desktop

import (
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

// gitLine shows the repository of a thread's folder below the composer. Outside
// a repository it shows nothing, and takes no room.
func gitLine(c *ui.Context, g workspace.Git) {
	if !g.Repo() {
		return
	}
	t := c.Theme()
	color := faint(t)
	item := func(icon *ui.SVG, text, label string) {
		ui.Row(c).Gap(4).Shrink(1).MinWidth(0).Label(label).Tooltip(label).Children(func() {
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
	})
}
