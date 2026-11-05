package desktop

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/worktree"
)

// workOnRowLimit keeps a repository with thousands of branches cheap to draw.
const workOnRowLimit = 40

// workOn is the state of the new-thread form's choice of what to work on. Its zero
// target is the project's own folder.
type workOn struct {
	projectID   string // what the rest was chosen for
	query, seen string
	target      worktree.Target
	fromTyping  bool // the target follows the search box, not a chosen row
	base        string
	suggested   string // the new branch's name, unless the user names it
	opening     string // the operation whose thread opens when it is done
}

var pullRequestQuery = regexp.MustCompile(`^#(\d{1,7})$`)

// effective is the target with what the form supplies.
func (w workOn) effective() worktree.Target {
	t := w.target
	if t.Kind == worktree.KindNew {
		t.Base = strings.TrimSpace(w.base)
		if t.Branch == "" {
			t.Branch = w.suggested
		}
	}
	return t
}

// typed follows the search box: its text names a pull request, a branch that
// exists, or else a new branch. Choosing a row overrides this, and clearing the box
// takes it back.
func (w *workOn) typed(listing *app.ProjectWorktrees) {
	if w.query == w.seen {
		return
	}
	w.seen = w.query
	q := strings.TrimSpace(w.query)
	if q == "" {
		switch {
		case w.fromTyping:
			w.target, w.fromTyping = worktree.Target{}, false
		case w.target.Kind == worktree.KindNew:
			w.target.Branch = "" // A chosen row, which says what the box says.
		}
		return
	}
	w.fromTyping = true
	if m := pullRequestQuery.FindStringSubmatch(q); m != nil {
		number, _ := strconv.Atoi(m[1])
		w.target = worktree.Target{Kind: worktree.KindPullRequest, Number: number}
		return
	}
	if b, ok := namedBranch(listing, q); ok {
		// A branch in a checkout is that checkout's row.
		switch b.Worktree {
		case "":
			w.target = worktree.Target{Kind: worktree.KindBranch, Branch: b.Name, Remote: b.Remote}
		case listing.Root:
			w.target = worktree.Target{}
		default:
			w.target = worktree.Target{Kind: worktree.KindPath, Path: b.Worktree}
		}
		return
	}
	w.target = worktree.Target{Kind: worktree.KindNew, Branch: q}
}

// namedBranch finds a listed branch by its name, preferring a local one, or by a
// remote's name for it, such as origin/main.
func namedBranch(listing *app.ProjectWorktrees, name string) (worktree.Branch, bool) {
	var found *worktree.Branch
	for i, b := range listing.Branches {
		switch {
		case b.Remote == "" && b.Name == name:
			return b, true
		case found == nil && (b.Name == name || b.Upstream == name || (b.Remote != "" && b.Remote+"/"+b.Name == name)):
			found = &listing.Branches[i]
		}
	}
	if found == nil {
		return worktree.Branch{}, false
	}
	return *found, true
}

// workOnReady reports whether the thread can be created: nothing needs resolving,
// or its plan is known and has no problem.
func workOnReady(s app.Snapshot, projectID string, target worktree.Target) bool {
	if target == (worktree.Target{}) {
		return true
	}
	p := s.WorktreePreview
	return p.ProjectID == projectID && p.Target == target && p.Done && p.Error == ""
}

// workOnField lets a thread in a repository's project choose what to work on, and
// returns the target, which is zero for the project's folder. Outside a repository
// it takes no room.
func (v *View) workOnField(c *ui.Context, s app.Snapshot) worktree.Target {
	if v.work.projectID != v.projectID {
		v.work = workOn{projectID: v.projectID, suggested: worktree.SuggestBranch()}
		if v.projectID != "" {
			v.app.RefreshWorktrees(v.projectID, true)
		}
	}
	listing := s.Worktrees[v.projectID]
	target := worktree.Target{}
	if v.projectID != "" && listing != nil && listing.Repo() {
		v.work.typed(listing)
		target = v.work.effective()
	}
	switch {
	case target != (worktree.Target{}):
		v.app.PreviewWorktree(v.projectID, target)
	case s.WorktreePreview.Target != (worktree.Target{}):
		v.app.ClearWorktreePreview()
	}
	t := c.Theme()
	if listing != nil && listing.Error != "" && v.projectID != "" {
		ui.Text(c, "Mu can't read this project's repository: "+listing.Error).FontSize(12.5).TextColor(t.Danger)
	}
	if v.projectID == "" || listing == nil || !listing.Repo() {
		return target
	}
	field(c, "Work on", "", func() {
		ui.TextInput(c.Key("work-on-search"), &v.work.query).Label("Search or name a new branch").Placeholder("Search, name a new branch, or #123")
		ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
			ui.Scroll(c).Label("What to work on").MaxHeight(208).Children(func() { v.workOnRows(c, s, listing) })
		})
		if v.work.target.Kind == worktree.KindNew {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "Start from").FontSize(12.5).TextColor(t.TextMuted).Shrink(0)
				ui.TextInput(c.Key("work-on-base"), &v.work.base).Label("Start from").Placeholder(listing.DefaultBase).Grow(1).MinWidth(0)
			})
		}
	})
	return target
}

// workOnPreview says in one sentence what creating the thread will do. It stays in
// view with the action, however long the list above it is. Without a choice to make
// it says nothing.
func (v *View) workOnPreview(c *ui.Context, s app.Snapshot, target worktree.Target) {
	t := c.Theme()
	listing := s.Worktrees[v.projectID]
	if v.projectID == "" || listing == nil || !listing.Repo() {
		return
	}
	text := func(message string) ui.Element {
		return ui.Text(c, message).FontSize(12.5).TextColor(t.TextMuted)
	}
	if target == (worktree.Target{}) {
		text("Works in the project's folder, shared by every thread that uses it. No Git changes.")
		return
	}
	p := s.WorktreePreview
	if p.ProjectID != v.projectID || p.Target != target || !p.Done {
		ui.Row(c).Gap(8).Children(func() {
			ui.Spinner(c).Size(12, 12).Label("Checking")
			text("Checking…")
		})
		return
	}
	if p.Error != "" {
		text(sentence(p.Error)).TextColor(t.Danger)
		return
	}
	// However long the names, the action stays on the screen.
	ui.Scroll(c).MaxHeight(96).Shrink(1).MinHeight(0).Gap(4).Children(func() {
		text(planSentence(p.Plan, listing))
		for _, warning := range p.Plan.Warnings {
			text(warning).TextColor(warnColor(t))
		}
	})
}

// planSentence describes a plan for reading.
func planSentence(p worktree.Plan, listing *app.ProjectWorktrees) string {
	where := shortPath(p.Path)
	switch p.Action {
	case worktree.ActionAttach:
		if p.Path == listing.Root {
			return "Works in the project's folder, where " + p.Branch + " is checked out, shared by every thread that uses it. No Git changes."
		}
		return "Uses the existing worktree " + where + " as it is, and shares its files with anything else working there. No Git changes."
	case worktree.ActionNewBranch:
		fetched := ""
		if p.Fetch != nil {
			fetched = ", fetched first"
		}
		return "Creates " + where + " on a new branch " + p.Branch + " from " + p.StartLabel + fetched + "."
	case worktree.ActionLocalBranch:
		if p.FastForward != nil {
			return "Creates " + where + " on the local branch " + p.Branch + ", fast-forwarded to " + p.StartLabel + " if it can be."
		}
		return "Creates " + where + " on the local branch " + p.Branch + "."
	case worktree.ActionTrackRemote:
		return "Fetches " + p.StartLabel + " and creates " + where + " on a new local branch " + p.Branch + " tracking it."
	case worktree.ActionDetached:
		return "Fetches " + p.StartLabel + " from " + p.Fetch.Remote + " and creates " + where + ", detached at " + p.Start[:min(len(p.Start), 7)] + ", to review or test."
	}
	return ""
}

// workOnRow is one choice in the list.
func (v *View) workOnRow(c *ui.Context, key string, icon *ui.SVG, title, detail string, target worktree.Target, disabled bool) {
	t := c.Theme()
	selected := v.work.target == target
	row := ui.ButtonBase(c.Key("work-"+key)).Transition(colorMotion).Label("Work on: "+title).Checked(selected).Disabled(disabled).FillWidth().Padding(6, 12).Gap(10)
	row.Background(rowColor(t, selected, row.Hovered() && !disabled))
	row.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, title).FontSize(13).SingleLine()
			if detail != "" {
				ui.Text(c, detail).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
			}
		})
		if selected {
			ui.Icon(c, checkIcon).Size(14, 14).TextColor(t.Accent).Shrink(0)
		}
	}).OnClick(func() { v.work.target, v.work.fromTyping = target, false })
}

// typedBranch is what the search box names as a new branch, if anything: not a pull
// request or a branch that exists.
func typedBranch(query string, listing *app.ProjectWorktrees) string {
	q := strings.TrimSpace(query)
	if pullRequestQuery.MatchString(q) {
		return ""
	}
	if _, ok := namedBranch(listing, q); ok {
		return ""
	}
	return q
}

func (v *View) workOnHeading(c *ui.Context, text string) {
	ui.Text(c, text).Padding(8, 12, 3, 12).FontSize(11.5).FontWeight(500).TextColor(faint(c.Theme())).SingleLine()
}

// workOnRows lists the choices the search matches.
func (v *View) workOnRows(c *ui.Context, s app.Snapshot, listing *app.ProjectWorktrees) {
	q := strings.ToLower(strings.TrimSpace(v.work.query))
	matches := func(text ...string) bool {
		return q == "" || strings.Contains(strings.ToLower(strings.Join(text, " ")), q)
	}
	if matches("project folder") || v.work.target == (worktree.Target{}) {
		detail := "Shared by every thread that uses it"
		for _, w := range listing.Worktrees {
			if w.Path == listing.Root {
				detail = w.Name() + " · " + detail
			}
		}
		v.workOnRow(c, "project-folder", folderIcon, "Project folder", detail, worktree.Target{}, false)
	}
	title := "New branch (automatic name)"
	typed := typedBranch(v.work.query, listing)
	if typed != "" {
		title = "New branch “" + typed + "”"
	}
	base := strings.TrimSpace(v.work.base)
	if base == "" {
		base = listing.DefaultBase
	}
	v.workOnRow(c, "new", branchIcon, title, "A new worktree, from "+base, worktree.Target{Kind: worktree.KindNew, Branch: typed}, false)

	shown := 0
	var worktrees []worktree.Worktree
	for _, w := range listing.Worktrees {
		if w.Path != listing.Root && matches(w.Name(), w.Path) {
			worktrees = append(worktrees, w)
		}
	}
	if len(worktrees) > 0 {
		v.workOnHeading(c, "Worktrees")
	}
	for _, w := range worktrees {
		detail := shortPath(w.Path)
		switch {
		case w.Missing:
			detail = "Its folder is gone · " + detail
		case w.Broken:
			detail = "Git no longer recognizes it · " + detail
		case w.Interrupted:
			detail = "Git never finished checking it out · " + detail
		}
		v.workOnRow(c, "worktree-"+w.Path, folderIcon, w.Name(), detail, worktree.Target{Kind: worktree.KindPath, Path: w.Path}, w.Missing || w.Broken || w.Interrupted)
	}

	if m := pullRequestQuery.FindStringSubmatch(strings.TrimSpace(v.work.query)); m != nil {
		number, _ := strconv.Atoi(m[1])
		listed := false
		for _, p := range listing.PullRequests {
			listed = listed || p.Number == number
		}
		if !listed {
			v.workOnHeading(c, "Pull requests")
			v.workOnRow(c, "pr-"+m[1], pullRequestIcon, "PR #"+m[1], "Fetch this pull request by number", worktree.Target{Kind: worktree.KindPullRequest, Number: number}, false)
		}
	}
	heading := false
	for _, p := range listing.PullRequests {
		if !matches("#"+strconv.Itoa(p.Number), p.Title, p.Author, p.Head) {
			continue
		}
		if !heading {
			v.workOnHeading(c, "Pull requests")
			heading = true
		}
		if shown++; shown > workOnRowLimit {
			break
		}
		detail := p.Head + " → " + p.Base
		if p.Author != "" {
			detail = p.Author + " · " + detail
		}
		if p.Draft {
			detail += " · Draft"
		}
		if p.Fork {
			detail += " · Fork"
		}
		v.workOnRow(c, "pr-"+strconv.Itoa(p.Number), pullRequestIcon, fmt.Sprintf("#%d %s", p.Number, p.Title), detail, worktree.Target{Kind: worktree.KindPullRequest, Number: p.Number}, false)
	}

	heading, shown = false, 0
	more := 0
	for _, b := range listing.Branches {
		// A branch in a checkout is listed as that checkout.
		if b.Worktree != "" || !matches(b.Name, b.Remote) {
			continue
		}
		if shown >= workOnRowLimit {
			more++
			continue
		}
		shown++
		if !heading {
			v.workOnHeading(c, "Branches")
			heading = true
		}
		v.workOnRow(c, "branch-"+b.Remote+"/"+b.Name, branchIcon, b.Name, branchDetail(b), worktree.Target{Kind: worktree.KindBranch, Branch: b.Name, Remote: b.Remote}, false)
	}
	if more > 0 {
		ui.Text(c, itoa(more)+" more branches. Type to narrow them down.").Padding(6, 12).FontSize(11.5).TextColor(faint(c.Theme()))
	}
}

// branchDetail says where a branch is, and how it compares with its upstream.
func branchDetail(b worktree.Branch) string {
	var parts []string
	switch {
	case b.Unfetched:
		parts = append(parts, "On "+b.Remote+", not fetched yet")
	case b.Remote != "":
		parts = append(parts, "On "+b.Remote)
	default:
		parts = append(parts, "Local")
	}
	if b.Ahead > 0 {
		parts = append(parts, itoa(b.Ahead)+" ahead")
	}
	if b.Behind > 0 {
		parts = append(parts, itoa(b.Behind)+" behind")
	}
	return strings.Join(parts, " · ")
}

// collectWorktreeOperations finishes operations that are done, and reports whether
// there were any.
func (v *View) collectWorktreeOperations(s app.Snapshot) (changed bool) {
	// Someone who left the form doesn't want a thread opening by itself.
	if v.work.opening != "" && (!v.creating || v.settingsOpen || v.importOpen) {
		v.work.opening = ""
	}
	if !v.creating && s.WorktreePreview.Target != (worktree.Target{}) {
		v.app.ClearWorktreePreview()
	}
	for _, op := range s.WorktreeOperations {
		if !op.Done {
			continue
		}
		changed = true
		v.app.ForgetWorktreeOperation(op.ID)
		if op.Deleting {
			v.finishDeleteWorktree(op)
			continue
		}
		wanted := v.work.opening == op.ID
		if wanted {
			v.work.opening = ""
		}
		if op.Error != "" {
			v.err = "Could not start on " + op.Label + ": " + sentence(op.Error)
			continue
		}
		if len(op.Notes) > 0 {
			v.notice = strings.Join(op.Notes, " ")
		}
		if wanted {
			v.archived = false
			v.selectThread(op.ThreadID)
		}
	}
	return changed
}

// worktreeOperationRows shows a project's operations under way, and reports whether
// there are any.
func (v *View) worktreeOperationRows(c *ui.Context, s app.Snapshot, projectID string) (any bool) {
	for _, op := range s.WorktreeOperations {
		if op.ProjectID != projectID || op.Done {
			continue
		}
		any = true
		text := "Setting up " + op.Label + "…"
		if op.Deleting {
			text = "Deleting " + op.Label + "…"
		}
		ui.Row(c.Key("worktree-op-"+op.ID)).Gap(8).Padding(6, 8).Children(func() {
			ui.Spinner(c).Size(12, 12).Label(text)
			ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().Grow(1).MinWidth(0)
		})
	}
	return any
}

// noticeBox says something worth knowing that isn't an error.
func (v *View) noticeBox(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Shrink(0).Margin(8, 16, 0, 16).Padding(6, 6, 6, 12).Gap(10).Radius(6).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Scroll(c).Grow(1).MinWidth(0).MaxHeight(96).Children(func() {
			ui.Text(c, v.notice).Selectable().FontSize(12.5)
		})
		iconButton(c, "Dismiss notice", closeIcon).OnClick(func() { v.notice = "" })
	})
}

// worktreeSettings chooses where new worktrees go.
func (v *View) worktreeSettings(c *ui.Context, s app.Snapshot) {
	t := c.Theme()
	ui.Column(c).Gap(2).Margin(12, 0, 0, 0).Children(func() {
		ui.Text(c, "Worktree folder").FontSize(13).FontWeight(500)
		ui.Text(c, "Where new worktrees go, as a complete path. Leave it empty to use the path shown. Existing worktrees stay where they are.").FontSize(11.5).TextColor(t.TextMuted)
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.TextInput(c.Key("worktree-template"), &v.worktreeTemplate).Label("Worktree folder template").Placeholder(s.DefaultWorktreeTemplate).Grow(1).MinWidth(120)
		ui.Button(c, "Save folder").Shrink(0).Disabled(strings.TrimSpace(v.worktreeTemplate) == s.WorktreeTemplate).OnClick(func() {
			if err := v.app.SetWorktreeTemplate(v.worktreeTemplate); err != nil {
				v.err = sentence(err.Error())
				return
			}
			v.worktreeTemplate, v.err = v.app.Snapshot().WorktreeTemplate, ""
		})
	})
	worktreeVariables(c)
}

// worktreeVariables explains what a template can use, with examples.
func worktreeVariables(c *ui.Context) {
	t := c.Theme()
	note := func(text string) {
		ui.Text(c, text).FontSize(11.5).TextColor(t.TextMuted)
	}
	ui.Column(c).Gap(6).Children(func() {
		note("You can use these variables. The examples are for a project in ~/dev/app:")
		ui.Column(c).Gap(4).Children(func() {
			for _, variable := range [][2]string{
				{"{repo}", "The repository's name, from the folder of its main checkout: app"},
				{"{repoPath}", "The full path of the project's checkout: ~/dev/app"},
				{"{checkoutParent}", "The folder that checkout is in: ~/dev"},
				{"{branch}", "The branch, made safe for a path: feature/login becomes feature-login. A detached pull request uses pr-123."},
			} {
				ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
					ui.Text(c, variable[0]).Selectable().Font("monospace").FontSize(11.5).Width(112).Shrink(0)
					ui.Text(c, variable[1]).FontSize(11.5).TextColor(t.TextMuted).Grow(1).MinWidth(0)
				})
			}
		})
		note("~ means your home folder, and a relative path starts at the checkout. If the folder already exists, its name gets -2, -3 and so on.")
	})
}

// worktreeEditor is the state of the worktree dialogs.
type worktreeEditor struct {
	open       bool
	projectID  string
	confirming bool
	target     worktree.Worktree
	branch     bool // also delete the branch
	deleting   string
	err        string
}

// openWorktrees lists a project's worktrees.
func (v *View) openWorktrees(projectID string) {
	v.worktrees.open, v.worktrees.projectID = true, projectID
	v.app.RefreshWorktrees(projectID, false)
}

func (v *View) confirmDeleteWorktree(projectID string, target worktree.Worktree) {
	v.worktrees.confirming, v.worktrees.projectID, v.worktrees.target, v.worktrees.branch, v.worktrees.err, v.worktrees.deleting = true, projectID, target, false, "", ""
}

// deleteWorktreeItem offers to delete the linked worktree a thread works in.
func (v *View) deleteWorktreeItem(m *ui.Menu, thread app.ThreadView, busy bool) {
	if thread.Worktree.Path == "" || thread.Worktree.Main {
		return
	}
	if m.Item("Delete worktree…").Disabled(busy).Chosen() {
		v.confirmDeleteWorktree(thread.ProjectID, thread.Worktree)
	}
}

func (v *View) finishDeleteWorktree(op app.WorktreeOperation) {
	if op.Error != "" {
		if v.worktrees.deleting == op.ID && v.worktrees.confirming {
			v.worktrees.deleting, v.worktrees.err = "", sentence(op.Error)
		} else {
			v.err = "Could not delete " + op.Label + ": " + sentence(op.Error)
		}
		v.loadSelected() // Its session closed only if it got as far as removing it.
		return
	}
	if v.worktrees.deleting == op.ID {
		v.worktrees.deleting, v.worktrees.confirming = "", false
	}
	for _, id := range op.Archived {
		if v.selected == id {
			v.selected = ""
		}
	}
	v.notice = strings.Join(append([]string{"Deleted the worktree " + op.Label + "."}, op.Notes...), " ")
}

// worktreesDialog lists a project's worktrees.
func (v *View) worktreesDialog(c *ui.Context, s app.Snapshot) {
	w := &v.worktrees
	t := c.Theme()
	modal(c, &w.open, func() {
		listing := s.Worktrees[w.projectID]
		ui.Column(c).Width(520).MaxWidthPercent(100).Gap(12).MinHeight(0).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, "Worktrees").FontSize(14).FontWeight(500)
				ui.Text(c, "Checkouts of this project's repository, each with its own files and branch.").FontSize(12).TextColor(t.TextMuted)
			})
			if listing != nil && listing.Error != "" {
				ui.Text(c, "This may be out of date. Mu can't read the repository: "+listing.Error).FontSize(12).TextColor(t.Danger)
			}
			ui.Column(c).Radius(8).Border(1, t.Border).Clip().Shrink(1).MinHeight(0).Children(func() {
				ui.Scroll(c).Label("Worktrees").Shrink(1).MinHeight(0).Children(func() {
					shown := 0
					if listing != nil {
						for _, tree := range listing.Worktrees {
							v.worktreeRow(c, s, listing, tree, shown > 0)
							shown++
						}
					}
					if shown == 0 {
						ui.Text(c, "No worktrees.").Padding(12, 14).FontSize(12.5).TextColor(t.TextMuted)
					}
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, "Close").OnClick(func() { w.open = false })
			})
		})
	})
}

func (v *View) worktreeRow(c *ui.Context, s app.Snapshot, listing *app.ProjectWorktrees, tree worktree.Worktree, divider bool) {
	t := c.Theme()
	threads := threadsInWorktree(s, tree.Path)
	row := ui.Row(c.Key(tree.Path)).Padding(9, 12, 9, 14).Gap(8)
	if divider {
		row.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, tree.Name()).FontSize(13).FontWeight(500).SingleLine().Shrink(1).MinWidth(0)
				switch {
				case tree.Path == listing.Root:
					chip(c, "Project folder", t.TextMuted)
				case tree.Main:
					chip(c, "Primary", t.TextMuted)
				}
				if tree.Locked {
					chip(c, "Locked", t.TextMuted)
				}
				if tree.Missing {
					chip(c, "Folder gone", warnColor(t))
				}
				if tree.Broken {
					chip(c, "Not recognized", warnColor(t))
				}
			})
			detail := shortPath(tree.Path)
			if threads > 0 {
				detail = threadCount(threads) + " · " + detail
			}
			ui.Text(c, detail).SingleLine().FontSize(11.5).TextColor(t.TextMuted).Tooltip(tree.Path)
		})
		if !tree.Missing && !tree.Broken {
			ui.Button(c, "New thread").Shrink(0).Label("New thread in " + tree.Name()).OnClick(func() { v.openNewOnWorktree(listing, tree) })
		}
		if !tree.Main && tree.Path != listing.Root {
			ui.Button(c, "Delete…").Shrink(0).Label("Delete worktree " + tree.Name()).TextColor(t.Danger).OnClick(func() { v.confirmDeleteWorktree(v.worktrees.projectID, tree) })
		}
	})
}

// openNewOnWorktree opens the new-thread form with this checkout chosen.
func (v *View) openNewOnWorktree(listing *app.ProjectWorktrees, tree worktree.Worktree) {
	projectID := v.worktrees.projectID
	v.worktrees.open = false
	v.openNewIn(projectID)
	v.work = workOn{projectID: projectID, suggested: worktree.SuggestBranch()}
	if tree.Path != listing.Root {
		v.work.target = worktree.Target{Kind: worktree.KindPath, Path: tree.Path}
	}
	v.app.RefreshWorktrees(projectID, true)
}

func threadCount(n int) string {
	if n == 1 {
		return "1 thread"
	}
	return itoa(n) + " threads"
}

// threadsInWorktree counts the threads that aren't archived in a folder.
func threadsInWorktree(s app.Snapshot, path string) (n int) {
	for _, t := range s.Threads {
		if !t.Archived && worktree.Contains(path, t.Cwd) {
			n++
		}
	}
	return n
}

// deleteWorktreeDialog says what deleting a worktree does and doesn't touch.
func (v *View) deleteWorktreeDialog(c *ui.Context, s app.Snapshot) {
	w := &v.worktrees
	t := c.Theme()
	modal(c, &w.confirming, func() {
		tree := w.target
		name := tree.Name()
		ui.Text(c, "Delete the worktree “"+name+"”?").FontSize(14).FontWeight(500).MaxWidth(440)
		body := "Git removes this folder. It refuses if the folder has uncommitted changes or untracked files; ignored files, such as build output, go with it."
		if tree.Missing {
			body = "Its folder is already gone, so Git only forgets the worktree."
		}
		ui.Text(c, body).FontSize(13).TextColor(t.TextMuted).MaxWidth(440)
		ui.Text(c, shortPath(tree.Path)).Font("monospace").FontSize(11.5).TextColor(t.TextMuted).MaxWidth(440).SingleLine().Tooltip(tree.Path)
		if n := threadsInWorktree(s, tree.Path); n > 0 {
			text := "Its thread is archived. The agent keeps the session."
			if n > 1 {
				text = "Its " + itoa(n) + " threads are archived. The agent keeps the sessions."
			}
			ui.Text(c, text).FontSize(13).TextColor(t.TextMuted).MaxWidth(440)
		}
		if tree.Branch != "" {
			ui.Checkbox(c, &w.branch, "Also delete the branch "+tree.Branch+", if it is fully merged").MaxWidth(440)
		}
		if w.err != "" {
			ui.Text(c, w.err).FontSize(12).TextColor(t.Danger).MaxWidth(440)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			if w.deleting != "" {
				ui.Spinner(c).Size(14, 14).Label("Deleting the worktree")
			}
			ui.Button(c, "Keep worktree").Disabled(w.deleting != "").OnClick(func() { w.confirming = false })
			ui.Button(c, "Delete worktree").TextColor(t.Danger).Disabled(w.deleting != "").OnClick(func() {
				id, err := v.app.DeleteWorktree(w.projectID, tree.Path, w.branch && tree.Branch != "")
				if err != nil {
					w.err = sentence(err.Error())
					return
				}
				w.deleting, w.err = id, ""
			})
		})
	})
}
