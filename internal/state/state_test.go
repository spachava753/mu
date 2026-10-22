package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistenceAndExclusiveLock(t *testing.T) {
	dir := t.TempDir()
	s, data, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Threads) != 0 || len(data.Agents) != 0 {
		t.Fatal("new store is not empty", data)
	}
	if second, _, err := Open(dir); err == nil {
		_ = second.Close()
		t.Fatal("second writer acquired lock")
	}
	data.Threads = []Thread{{ID: ID(), Title: "hello", SessionID: "agent-owned"}}
	if err := s.Save(data); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, got, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got.Threads[0].SessionID != "agent-owned" {
		t.Fatal(got)
	}
	info, err := os.Stat(filepath.Join(dir, "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private history mode: %o", info.Mode().Perm())
	}
}

func TestBadHistoryIsNeverOverwritten(t *testing.T) {
	for _, bad := range []string{`{broken`, `{"threads":"invalid"}`, `{"threads":[{"id":"x"},{"id":"x"}]}`, `{"threads":[{"id":"x","sessionId":"s"},{"id":"y","sessionId":"s"}]}`} {
		dir := t.TempDir()
		path := filepath.Join(dir, "threads.json")
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if s, _, err := Open(dir); err == nil {
			_ = s.Close()
			t.Fatal("opened invalid history")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != bad {
			t.Fatal("changed bad history", err)
		}
		// Opening again must fail on parsing, not on a leaked lock.
		if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		s, _, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
}
