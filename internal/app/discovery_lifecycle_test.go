package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func waitCall(t *testing.T, command, method string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(fixtureCalls(t, command), method) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fixture never received", method)
}

func TestShutdownCancelsDiscoveryAndFork(t *testing.T) {
	for _, operation := range []string{"list", "fork"} {
		t.Run(operation, func(t *testing.T) {
			command := testutil.Agent(t)
			a := openTest(t, t.TempDir())
			if operation == "list" {
				id, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command + " wait-list"}, true)
				if err != nil {
					t.Fatal(err)
				}
				// Ensure the trace exists before polling the asynchronous discovery.
				if err := os.MkdirAll(filepath.Join(filepath.Dir(command), "fixture-sessions"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(filepath.Dir(command), "fixture-sessions", "calls.jsonl"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := a.Discover(id); err != nil {
					t.Fatal(err)
				}
			} else {
				id := createTest(t, a, command+" wait-fork")
				connected(t, a, id)
				finished := make(chan error, 1)
				go func() { _, err := a.Fork(id); finished <- err }()
				t.Cleanup(func() {
					select {
					case err := <-finished:
						if err == nil {
							t.Error("cancelled fork succeeded")
						}
					case <-time.After(time.Second):
						t.Error("fork worker leaked")
					}
				})
			}
			waitCall(t, command, "session/"+operation)
			started := time.Now()
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("shutdown waited for full request timeout")
			}
		})
	}
}

func TestSupersededDiscoveryCannotReplaceCatalog(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command + " wait-list"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Discover(id); err != nil {
		t.Fatal(err)
	}
	first := a.Snapshot().Catalog.Generation
	if _, err := a.SaveAgent(state.AgentConfig{ID: id, Name: "Fixture", Command: command}, true); err != nil {
		t.Fatal(err)
	}
	c := discoverDone(t, a, id)
	if c.Generation == first || !c.Complete || c.Error != "" || c.Agent.Command != command {
		t.Fatal("stale discovery replaced result", c)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if c = a.Snapshot().Catalog; !c.Complete || c.Error != "" {
		t.Fatal("cancelled discovery overwrote completed catalog", c)
	}
}

func TestImportRollsBackOnSaveFailure(t *testing.T) {
	command, dir := testutil.Agent(t), t.TempDir()
	a := openTest(t, dir)
	configID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	source, err := a.CreateWithAgent(configID, "")
	if err != nil {
		t.Fatal(err)
	}
	v := connected(t, a, source)
	if err := a.Delete(source); err != nil {
		t.Fatal(err)
	}
	// Discovery is separate from the removed thread's tracked close work.
	c := discoverDone(t, a, configID)
	if c.Error != "" || len(c.Sessions) != 1 {
		t.Fatal(c)
	}
	if _, err := a.Import(c.Generation, []string{v.SessionID, "not-listed"}); err == nil || len(a.Snapshot().Threads) != 0 {
		t.Fatal("partial import committed")
	}
	path := filepath.Join(dir, "threads.json")
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path); _ = os.Rename(path+".saved", path) }()
	if _, err := a.Import(c.Generation, []string{v.SessionID}); err == nil {
		t.Fatal("failed save accepted")
	}
	if len(a.Snapshot().Threads) != 0 || a.Snapshot().StorageError == "" {
		t.Fatal("phantom imported reference or hidden storage error")
	}
}
