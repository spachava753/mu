package app_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/mu/internal/app"
)

// TestQAStalledAgent is a subprocess fixture, not an external provider.
func TestQAStalledAgent(t *testing.T) {
	if os.Args[len(os.Args)-1] != "mu-qa-stalled-agent" {
		t.Skip("subprocess fixture")
	}
	if err := os.WriteFile("qa-agent-started", nil, 0600); err != nil {
		os.Exit(2)
	}
	_, _ = io.Copy(io.Discard, os.Stdin) // Consume initialize, deliberately never answer.
	os.Exit(0)
}

func TestQACancelConnecting(t *testing.T) {
	for _, closeApp := range []bool{false, true} {
		name := "stop"
		if closeApp {
			name = "close"
		}
		t.Run(name, func(t *testing.T) {
			storage, workspace := t.TempDir(), t.TempDir()
			a, err := app.Open(storage)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' -test.run=^TestQAStalledAgent$ -- mu-qa-stalled-agent"
			id, err := a.Create(command, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.Send(id, "must not reach a model"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err = os.Stat(filepath.Join(workspace, "qa-agent-started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("stalled helper did not start")
				}
				time.Sleep(5 * time.Millisecond)
			}
			start := time.Now()
			if closeApp {
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				a.Stop(id)
				for qaThread(t, a, id).Running {
					if time.Since(start) > 5*time.Second {
						t.Fatal("Stop did not unblock ACP initialization")
					}
					time.Sleep(5 * time.Millisecond)
				}
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("shutdown waited for initialization timeout")
			}
			reopened, err := app.Open(storage)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			thread := qaThread(t, reopened, id)
			if thread.Running {
				t.Fatal("cancelled thread remained running after reopen")
			}
			if len(thread.Entries) != 0 {
				t.Fatalf("cancelled turn was persisted locally: %#v", thread.Entries)
			}
		})
	}
}

func qaThread(t *testing.T, a *app.App, id string) app.ThreadView {
	t.Helper()
	for _, thread := range a.Snapshot().Threads {
		if thread.ID == id {
			return thread
		}
	}
	t.Fatal("thread not found: ", id)
	return app.ThreadView{}
}
