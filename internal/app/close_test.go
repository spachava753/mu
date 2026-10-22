package app

import (
	"strings"
	"testing"
	"time"

	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func TestReloadWaitsForSessionCloseAndRejectsLateUpdates(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" slow-close")
	sendDone(t, a, id, "original")
	started := time.Now()
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if time.Since(started) < 120*time.Millisecond {
		t.Fatal("new connection raced session/close")
	}
	if !v.Connected || v.SessionError != "" || len(v.Entries) != 2 || lastText(v) != "Echo: original" {
		t.Fatal("late close update corrupted replay", v)
	}
}

func TestReimportWaitsForRemovedReferencesClose(t *testing.T) {
	a := openTest(t, t.TempDir())
	command, cwd := testutil.Agent(t)+" slow-close", t.TempDir()
	config, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.CreateWithAgent(config, cwd)
	if err != nil {
		t.Fatal(err)
	}
	v := connected(t, a, id)
	c := discoverDone(t, a, config, cwd)
	if c.Error != "" {
		t.Fatal(c)
	}
	started := time.Now()
	if err := a.Delete(id); err != nil {
		t.Fatal(err)
	}
	ids, err := a.Import(c.Generation, []string{v.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] == id {
		t.Fatal("did not create a new local reference")
	}
	connected(t, a, ids[0])
	if time.Since(started) < 120*time.Millisecond {
		t.Fatal("reimport raced the removed reference's session/close")
	}
}

func TestCloseFailureIsVisibleAndStillReleasesStore(t *testing.T) {
	command := testutil.Agent(t)
	for _, mode := range []string{"close-error", "wait-close"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			a := openTest(t, dir)
			id := createTest(t, a, command+" "+mode)
			connected(t, a, id)
			started := time.Now()
			if err := a.Archive(id, true); err != nil {
				t.Fatal(err)
			}
			v := await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
			if !strings.Contains(v.SessionError, "close session") || a.Snapshot().LifecycleError == "" {
				t.Fatal("close failure hidden", v)
			}
			if err := a.Close(); err == nil || !strings.Contains(err.Error(), "close session") {
				t.Fatal("shutdown did not report failed close", err)
			}
			if time.Since(started) > 5*time.Second {
				t.Fatal("close was unbounded")
			}
			b := openTest(t, dir)
			if len(b.Snapshot().Threads) != 1 {
				t.Fatal("close failure lost registry")
			}
		})
	}
}
