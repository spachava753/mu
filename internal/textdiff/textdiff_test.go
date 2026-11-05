package textdiff

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

func text(lines ...string) *string {
	s := strings.Join(lines, "\n") + "\n"
	return &s
}

func render(f File) string {
	var b strings.Builder
	for i, hunk := range f.Hunks {
		if i > 0 {
			b.WriteString("~\n")
		}
		for _, line := range hunk {
			b.WriteByte(" +-"[line.Op])
			b.WriteString(line.Text + "\n")
		}
	}
	return b.String()
}

func TestSmallEdit(t *testing.T) {
	files := Files([]Change{{Path: "a", Old: text("one", "two", "three"), New: *text("one", "TWO", "three")}})
	if got, want := render(files[0]), " one\n-two\n+TWO\n three\n"; got != want {
		t.Fatalf("got:\n%s", got)
	}
	if files[0].Added != 1 || files[0].Removed != 1 {
		t.Fatalf("counts %+v", files[0])
	}
}

func TestNewFileIsAllAdded(t *testing.T) {
	files := Files([]Change{{Path: "a", New: "x\ny\n"}})
	if got, want := render(files[0]), "+x\n+y\n"; got != want {
		t.Fatalf("got:\n%s", got)
	}
	if files[0].Added != 2 || files[0].Removed != 0 {
		t.Fatalf("counts %+v", files[0])
	}
}

func TestDeletionToNothing(t *testing.T) {
	files := Files([]Change{{Path: "a", Old: text("x"), New: ""}})
	if got := render(files[0]); got != "-x\n" {
		t.Fatalf("got:\n%s", got)
	}
}

func TestFarApartChangesAreSeparateHunks(t *testing.T) {
	var old, changed []string
	for i := range 40 {
		old = append(old, fmt.Sprint(i))
		changed = append(changed, fmt.Sprint(i))
	}
	changed[2], changed[35] = "two", "thirty-five"
	files := Files([]Change{{Path: "a", Old: text(old...), New: *text(changed...)}})
	if len(files[0].Hunks) != 2 {
		t.Fatalf("hunks %s", render(files[0]))
	}
	// Three lines of context around each, clipped at the start of the file.
	if got := len(files[0].Hunks[1]); got != 8 {
		t.Fatalf("second hunk %d lines:\n%s", got, render(files[0]))
	}
}

func TestChangesToOneFileAreGrouped(t *testing.T) {
	files := Files([]Change{
		{Path: "a", Old: text("one"), New: *text("ONE")},
		{Path: "b", Old: text("x"), New: *text("y")},
		{Path: "a", Old: text("two"), New: *text("TWO")},
	})
	if len(files) != 2 || files[0].Path != "a" || len(files[0].Hunks) != 2 || files[0].Added != 2 {
		t.Fatalf("files %+v", files)
	}
}

func TestIdenticalTextHasNoHunks(t *testing.T) {
	files := Files([]Change{{Path: "a", Old: text("same"), New: *text("same")}})
	if len(files) != 1 || len(files[0].Hunks) != 0 {
		t.Fatalf("files %+v", files)
	}
}

// Applying a diff has to give back both texts, whatever the changes.
func TestDiffReproducesBothSides(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	word := func() string { return fmt.Sprint(random.IntN(5)) }
	for range 2000 {
		var old, new []string
		for range random.IntN(30) {
			old = append(old, word())
		}
		new = slices.Clone(old)
		for range random.IntN(10) {
			switch i := random.IntN(len(new) + 1); random.IntN(3) {
			case 0:
				new = slices.Insert(new, i, word())
			case 1:
				if i < len(new) {
					new = slices.Delete(new, i, i+1)
				}
			default:
				if i < len(new) {
					new[i] = word()
				}
			}
		}
		var gotOld, gotNew []string
		for _, line := range diff(old, new) {
			if line.Op != Added {
				gotOld = append(gotOld, line.Text)
			}
			if line.Op != Removed {
				gotNew = append(gotNew, line.Text)
			}
		}
		if !slices.Equal(gotOld, old) || !slices.Equal(gotNew, new) {
			t.Fatalf("old %v new %v: got %v and %v", old, new, gotOld, gotNew)
		}
	}
}

func TestLargeOrVeryDifferentTextsStayFast(t *testing.T) {
	var a, b []string
	for i := range 9000 {
		a = append(a, fmt.Sprintf("old %d", i))
		b = append(b, fmt.Sprintf("new %d", i))
	}
	for name, change := range map[string]Change{
		"different": {Old: text(a...), New: *text(b...)},
		"huge":      {Old: text(slices.Repeat(a, 3)...), New: *text(slices.Repeat(b, 3)...)},
	} {
		began := time.Now()
		files := Files([]Change{change})
		if elapsed := time.Since(began); elapsed > 500*time.Millisecond {
			t.Errorf("%s took %s", name, elapsed)
		}
		if files[0].Added == 0 || files[0].Removed == 0 {
			t.Errorf("%s: %+v", name, files[0].Added)
		}
	}
}
