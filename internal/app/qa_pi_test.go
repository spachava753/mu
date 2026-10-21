package app_test

import (
	"io"
	"os"
	"os/exec"
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
				for qaPiThread(t, a, id).Running {
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
			thread := qaPiThread(t, reopened, id)
			if thread.Running {
				t.Fatal("cancelled thread remained running after reopen")
			}
			if len(thread.Entries) != 2 || !strings.Contains(thread.Entries[1].Text, "Stopped") {
				t.Fatalf("cancelled turn lacks persisted stop notice: %#v", thread.Entries)
			}
		})
	}
}

// TestQAPiLifecycle is intentionally opt-in: it uses existing Pi authentication
// and sends four tiny model prompts. All workspace and session data are temporary.
// Run: MU_QA_PI=1 go test ./internal/app -run TestQAPiLifecycle -v -timeout 5m
func TestQAPiLifecycle(t *testing.T) {
	if os.Getenv("MU_QA_PI") != "1" {
		t.Skip("set MU_QA_PI=1 to authorize real Pi provider requests")
	}
	pi, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs("../../.tools/node_modules/pi-acp/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(adapter); err != nil {
		t.Fatal("install the reviewed .tools/pi-acp adapter first: ", err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	authDir := os.Getenv("PI_CODING_AGENT_DIR")
	if authDir == "" {
		authDir = filepath.Join(realHome, ".pi", "agent")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".pi", "agent")
	if err = os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	// Pi alone reads authentication. Never read or log its contents here.
	if err = os.Symlink(filepath.Join(authDir, "auth.json"), filepath.Join(config, "auth.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"quietStartup":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(home, "pi-smoke")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nexec " + quote(pi) + " --no-extensions --no-skills --no-prompt-templates --no-context-files --no-mcp --no-tools --no-approve --offline --model openai/gpt-6-astra --thinking low \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", config)
	t.Setenv("PI_ACP_PI_COMMAND", wrapper)
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PI_SKIP_VERSION_CHECK", "1")
	t.Setenv("PI_TELEMETRY", "0")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(config, "sessions"))
	command := quote(node) + " " + quote(adapter)
	workspace, storage := t.TempDir(), t.TempDir()
	a, err := app.Open(storage)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			if err := a.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	first, err := a.Create(command, workspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Create(command, workspace)
	if err != nil {
		t.Fatal(err)
	}
	// Start both before waiting, exercising independent live adapter processes.
	if err = a.Send(first, "Remember my marker: MU_QA_AMBER_731. Reply with exactly MU_QA_AMBER_731."); err != nil {
		t.Fatal(err)
	}
	if err = a.Send(second, "Reply with exactly MU_QA_BLUE_249."); err != nil {
		t.Fatal(err)
	}
	one := qaWaitPi(t, a, first)
	two := qaWaitPi(t, a, second)
	qaExpectAnswer(t, one, "MU_QA_AMBER_731")
	qaExpectAnswer(t, two, "MU_QA_BLUE_249")
	if one.SessionID == "" || one.SessionID == two.SessionID {
		t.Fatal("threads did not receive independent sessions")
	}
	firstSession, originalCount := one.SessionID, len(one.Entries)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = app.Open(storage)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Send(first, "What marker did I ask you to remember? Reply with only the marker."); err != nil {
		t.Fatal(err)
	}
	resumed := qaWaitPi(t, a, first)
	qaExpectAnswer(t, resumed, "MU_QA_AMBER_731")
	if resumed.SessionID != firstSession {
		t.Fatal("resume changed ACP session ID")
	}
	if len(resumed.Entries) < originalCount+2 {
		t.Fatal("resume lost messages")
	}
	for i, entry := range one.Entries {
		if resumed.Entries[i] != entry {
			t.Fatal("resume changed existing transcript")
		}
	}
	users, assistants := 0, 0
	for _, entry := range resumed.Entries {
		if entry.Role == "user" {
			users++
		}
		if entry.Role == "assistant" {
			assistants++
		}
	}
	if users != 2 || assistants != 2 {
		t.Fatalf("resume duplicated history: %d users, %d assistants", users, assistants)
	}
	fork, err := a.Fork(first)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Send(fork, "What marker did I ask you to remember? Reply with only the marker."); err != nil {
		t.Fatal(err)
	}
	forked := qaWaitPi(t, a, fork)
	qaExpectAnswer(t, forked, "MU_QA_AMBER_731")
	if forked.SessionID == firstSession || forked.ParentID != first {
		t.Fatal("fork did not create independent linked session")
	}
	if len(qaPiThread(t, a, first).Entries) != len(resumed.Entries) {
		t.Fatal("fork mutated original transcript")
	}
	if err = a.Archive(first, true); err != nil {
		t.Fatal(err)
	}
	if !qaPiThread(t, a, first).Archived {
		t.Fatal("archive not reflected")
	}
	if err = a.Send(first, "must not run"); err == nil {
		t.Fatal("archived thread accepted a prompt")
	}
	if err = a.Archive(first, false); err != nil {
		t.Fatal(err)
	}
	// Adapter slash command verifies restored process/session without a model call.
	if err = a.Send(first, "/session"); err != nil {
		t.Fatal(err)
	}
	restored := qaWaitPi(t, a, first)
	if restored.SessionID != firstSession {
		t.Fatal("restore lost session identity")
	}
	if err = a.Delete(second); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = app.Open(storage)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range a.Snapshot().Threads {
		if thread.ID == second {
			t.Fatal("deleted thread returned after reopen")
		}
	}
	if len(a.Snapshot().Threads) != 2 {
		t.Fatal("unexpected surviving thread count")
	}
	t.Log("real Pi: concurrent threads, close/resume, transcript fork continuity, archive/restore/delete passed")
}

func qaPiThread(t *testing.T, a *app.App, id string) app.ThreadView {
	t.Helper()
	for _, thread := range a.Snapshot().Threads {
		if thread.ID == id {
			return thread
		}
	}
	t.Fatal("thread not found: ", id)
	return app.ThreadView{}
}
func qaWaitPi(t *testing.T, a *app.App, id string) app.ThreadView {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		thread := qaPiThread(t, a, id)
		if !thread.Running {
			if thread.Error != "" {
				t.Fatal(thread.Error)
			}
			return thread
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.Stop(id)
	t.Fatal("timed out waiting for Pi")
	return app.ThreadView{}
}
func qaExpectAnswer(t *testing.T, thread app.ThreadView, expected string) {
	t.Helper()
	for i := len(thread.Entries) - 1; i >= 0; i-- {
		if thread.Entries[i].Role == "assistant" {
			if strings.TrimSpace(thread.Entries[i].Text) != expected {
				t.Fatalf("answer = %q, want %q", thread.Entries[i].Text, expected)
			}
			return
		}
	}
	t.Fatalf("no assistant answer in %d entries", len(thread.Entries))
}
