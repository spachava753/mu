// Package textdiff turns before-and-after text into line changes for display. It
// has no dependencies on the rest of Mu, and its work is bounded, so that a large
// file can't stall whatever asks for a diff.
package textdiff

import (
	"slices"
	"strings"
)

// Op says what happened to a line.
type Op byte

const (
	Context Op = iota
	Added
	Removed
)

// Line is one line of a diff, without its line ending.
type Line struct {
	Op   Op
	Text string
}

// Hunk is a run of changes with the lines around them.
type Hunk []Line

// File is the changes to one file. Hunks are immutable once computed, so that
// snapshots can share them.
type File struct {
	Path           string
	Hunks          []Hunk
	Added, Removed int
}

// Change is a replacement within a file. Old is nil for text with nothing before
// it, such as a new file.
type Change struct {
	Path string
	Old  *string
	New  string
}

const (
	contextLines = 3
	// Beyond these, a change is shown as a whole replacement. Together they bound
	// the work: lines beyond maxLines aren't compared, and the search stops after
	// maxEdits differences.
	maxLines = 10000
	maxEdits = 500
)

// Files computes the diffs for a set of changes, grouping changes to the same file
// in the order they first appear.
func Files(changes []Change) []File {
	var files []File
	for _, change := range changes {
		i := slices.IndexFunc(files, func(f File) bool { return f.Path == change.Path })
		if i < 0 {
			files = append(files, File{Path: change.Path})
			i = len(files) - 1
		}
		old := ""
		if change.Old != nil {
			old = *change.Old
		}
		f := &files[i]
		for _, hunk := range hunks(lines(old), lines(change.New)) {
			f.Hunks = append(f.Hunks, hunk)
			for _, line := range hunk {
				switch line.Op {
				case Added:
					f.Added++
				case Removed:
					f.Removed++
				}
			}
		}
	}
	return files
}

func lines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// hunks diffs two texts and keeps the changes with a little context around them.
func hunks(old, new []string) []Hunk {
	all := diff(old, new)
	var out []Hunk
	for start := 0; start < len(all); {
		if all[start].Op == Context {
			start++
			continue
		}
		// Extend past changes separated by little enough context to read as one.
		end := start
		for i := start; i < len(all); i++ {
			if all[i].Op != Context {
				end = i
			} else if i-end > contextLines*2 {
				break
			}
		}
		first, last := max(start-contextLines, 0), min(end+contextLines+1, len(all))
		out = append(out, slices.Clone(all[first:last]))
		start = last
	}
	return out
}

// diff is every line of both texts, in order, each marked with what happened to
// it. A shared start and end are removed first, which is most of the work for the
// usual small edit.
func diff(old, new []string) []Line {
	prefix := 0
	for prefix < len(old) && prefix < len(new) && old[prefix] == new[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(new)-prefix && old[len(old)-1-suffix] == new[len(new)-1-suffix] {
		suffix++
	}
	out := make([]Line, 0, len(old)+len(new))
	for _, text := range old[:prefix] {
		out = append(out, Line{Context, text})
	}
	out = append(out, middle(old[prefix:len(old)-suffix], new[prefix:len(new)-suffix])...)
	for _, text := range old[len(old)-suffix:] {
		out = append(out, Line{Context, text})
	}
	return out
}

func replaced(old, new []string) []Line {
	out := make([]Line, 0, len(old)+len(new))
	for _, text := range old {
		out = append(out, Line{Removed, text})
	}
	for _, text := range new {
		out = append(out, Line{Added, text})
	}
	return out
}

// middle runs Myers' greedy algorithm, giving up on texts too large or too
// different for a line-by-line comparison to help a reader anyway.
func middle(old, new []string) []Line {
	n, m := len(old), len(new)
	if n == 0 || m == 0 || n > maxLines || m > maxLines {
		return replaced(old, new)
	}
	limit := min(n+m, maxEdits)
	offset := limit + 1
	furthest := make([]int, 2*limit+3)
	var trace [][]int // furthest as each round found it, to walk back through
	for d := 0; d <= limit; d++ {
		trace = append(trace, slices.Clone(furthest))
		for k := -d; k <= d; k += 2 {
			x := furthest[offset+k-1] + 1
			if k == -d || (k != d && furthest[offset+k-1] < furthest[offset+k+1]) {
				x = furthest[offset+k+1]
			}
			y := x - k
			for x < n && y < m && old[x] == new[y] {
				x++
				y++
			}
			furthest[offset+k] = x
			if x >= n && y >= m {
				return backtrack(old, new, trace, offset, k)
			}
		}
	}
	return replaced(old, new)
}

// backtrack follows the search back from the end, recovering the edits it found.
func backtrack(old, new []string, trace [][]int, offset, k int) []Line {
	reversed := make([]Line, 0, len(old)+len(new))
	x, y := len(old), len(new)
	for d := len(trace) - 1; d > 0; d-- {
		before := trace[d]
		previous := k - 1
		if k == -d || (k != d && before[offset+k-1] < before[offset+k+1]) {
			previous = k + 1
		}
		startX := before[offset+previous]
		startY := startX - previous
		for x > startX && y > startY {
			x--
			y--
			reversed = append(reversed, Line{Context, old[x]})
		}
		if x > startX {
			x--
			reversed = append(reversed, Line{Removed, old[x]})
		} else {
			y--
			reversed = append(reversed, Line{Added, new[y]})
		}
		k = previous
	}
	for x > 0 && y > 0 {
		x--
		y--
		reversed = append(reversed, Line{Context, old[x]})
	}
	slices.Reverse(reversed)
	return reversed
}
