package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spachava753/mu/internal/testutil"
)

func TestArchiveAndRestoreThreads(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	first, second, idle := createTest(t, a, command), createTest(t, a, command), createTest(t, a, command)
	connected(t, a, first)
	connected(t, a, second)
	before := len(fixtureCalls(t, command))
	result, err := a.ArchiveThreads([]string{first, second, "gone"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.Changed, []string{first, second}) || len(result.Skipped) != 0 {
		t.Fatalf("%+v", result)
	}
	for _, id := range []string{first, second} {
		if v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring }); !v.Archived || v.Connected {
			t.Fatalf("not archived and closed: %+v", v.Thread)
		}
	}
	if n := count(fixtureCalls(t, command)[before:], "session/close"); n != 2 {
		t.Fatalf("closes: %d", n)
	}
	if findThread(t, a, idle).Archived {
		t.Fatal("archived an unselected thread")
	}
	// Already archived threads are ignored; restoring changes them back.
	if result, err := a.ArchiveThreads([]string{first}, true); err != nil || len(result.Changed) != 0 {
		t.Fatal(result, err)
	}
	result, err = a.ArchiveThreads([]string{first, second, idle}, false)
	if err != nil || !slices.Equal(result.Changed, []string{first, second}) {
		t.Fatal(result, err)
	}
	if findThread(t, a, first).Archived {
		t.Fatal("not restored")
	}
}

func TestBulkActionsSkipBusyThreads(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	busy, idle, other := createTest(t, a, command), createTest(t, a, command), createTest(t, a, command)
	if err := a.Send(busy, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, busy, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	result, err := a.ArchiveThreads([]string{busy, idle}, true)
	if err != nil || !slices.Equal(result.Changed, []string{idle}) || !slices.Equal(result.Skipped, []string{busy}) {
		t.Fatal(result, err)
	}
	if findThread(t, a, busy).Archived {
		t.Fatal("archived a running thread")
	}
	result, err = a.DeleteThreads([]string{busy, other, "gone"})
	if err != nil || !slices.Equal(result.Changed, []string{other}) || !slices.Equal(result.Skipped, []string{busy}) {
		t.Fatal(result, err)
	}
	if v := a.Snapshot(); len(v.Threads) != 2 {
		t.Fatalf("threads: %d", len(v.Threads))
	}
	a.Stop(busy)
	await(t, a, busy, func(v ThreadView) bool { return !v.Running })
}

func TestDeleteThreadsRemovesOnlyReferences(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	first, second := createTest(t, a, command), createTest(t, a, command)
	connected(t, a, first)
	before := len(fixtureCalls(t, command))
	if _, err := a.DeleteThreads([]string{first, second}); err != nil {
		t.Fatal(err)
	}
	if n := len(a.Snapshot().Threads); n != 0 {
		t.Fatalf("threads: %d", n)
	}
	waitCall(t, command, "session/close")
	for _, call := range fixtureCalls(t, command)[before:] {
		if strings.Contains(call, "delete") {
			t.Fatalf("called agent-side deletion: %s", call)
		}
	}
}

// One save covers a bulk action: if it fails, nothing changes.
func TestBulkActionsAreAtomic(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	first, second := createTest(t, a, "unused-agent"), createTest(t, a, "unused-agent")
	path := filepath.Join(dir, "threads.json")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path); _ = os.Rename(path+".saved", path) }()
	if _, err := a.ArchiveThreads([]string{first, second}, true); err == nil {
		t.Fatal("failed save accepted")
	}
	if findThread(t, a, first).Archived || findThread(t, a, second).Archived {
		t.Fatal("archive kept after a failed save")
	}
	if _, err := a.DeleteThreads([]string{first, second}); err == nil {
		t.Fatal("failed save accepted")
	}
	if n := len(a.Snapshot().Threads); n != 2 {
		t.Fatalf("threads after a failed save: %d", n)
	}
}
