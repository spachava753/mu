package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

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

// pullRequestLink is where a pull request can be opened, if anywhere. The address
// goes to the desktop's URL handler, so only a web address will do.
func pullRequestLink(p workspace.PullRequest) string {
	if strings.HasPrefix(p.URL, "https://") || strings.HasPrefix(p.URL, "http://") {
		return p.URL
	}
	return ""
}

// gitLine shows the repository of a thread's folder below the composer, with the
// branch's pull request if it has one. Outside a repository it shows nothing, and
// takes no room.
func (v *View) gitLine(c *ui.Context, g workspace.Git, pr workspace.PullRequest) {
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
			link := pullRequestLink(pr)
			if link == "" {
				item(pullRequestIcon, text, text+": "+pr.Title).Shrink(0) // The names can be cut; this says little enough.
				return
			}
			button := ui.ButtonBase(c).Transition(colorMotion).Role(ui.RoleLink).Label(text + ": " + pr.Title).Tooltip(text + ": " + pr.Title).Cursor(ui.CursorPointer).Gap(4).Radius(3).Shrink(0)
			shade := color
			if button.Hovered() {
				shade = t.Text
			}
			button.Children(func() {
				ui.Icon(c, pullRequestIcon).Size(12, 12).TextColor(shade).Shrink(0)
				ui.Text(c, text).FontSize(12).TextColor(shade).SingleLine()
			}).OnClick(func() {
				c.OpenURLThen(link, func(err error) {
					if err != nil {
						v.fail(err)
					}
				})
			})
		}
	})
}
