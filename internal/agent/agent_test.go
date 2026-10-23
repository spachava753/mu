package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/testutil"
)

func TestCommandParsing(t *testing.T) {
	args, err := ParseCommand(`"/some path/agent" --model "a b" '$HOME'`)
	if err != nil || len(args) != 4 || args[0] != "/some path/agent" || args[2] != "a b" || args[3] != "$HOME" {
		t.Fatal(args, err)
	}
	for _, bad := range []string{"", `"unterminated`} {
		if _, err := ParseCommand(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestFileDelegation(t *testing.T) {
	ctx := context.Background()
	cwd, extra, other := t.TempDir(), t.TempDir(), t.TempDir()
	p := &Process{roots: map[acp.SessionId][]string{}, terminals: map[string]*terminal{}}
	p.OpenSession("s", cwd, []string{extra})
	p.OpenSession("other", other, nil)
	path := filepath.Join(cwd, "sub", "file.txt")
	_, err := p.WriteTextFile(ctx, acp.WriteTextFileRequest{SessionId: "s", Path: path, Content: "one\ntwo\nthree\n"})
	if err != nil {
		t.Fatal(err)
	}
	line, limit := 2, 1
	r, err := p.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: "s", Path: path, Line: &line, Limit: &limit})
	if err != nil || r.Content != "two\n" {
		t.Fatal(r, err)
	}
	line = 99
	r, err = p.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: "s", Path: path, Line: &line})
	if err != nil || r.Content != "" {
		t.Fatal(r, err)
	}
	// Additional directories are part of the session's root set.
	if _, err := p.WriteTextFile(ctx, acp.WriteTextFileRequest{SessionId: "s", Path: filepath.Join(extra, "notes.txt"), Content: "ok"}); err != nil {
		t.Fatal("additional root rejected", err)
	}
	// Roots are per session, even on a shared process.
	if _, err := p.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: "other", Path: path}); err == nil {
		t.Fatal("read another session's workspace")
	}
	p.ForgetSession("s")
	if _, err := p.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: "s", Path: path}); err == nil {
		t.Fatal("closed session kept file access")
	}
	p.OpenSession("s", cwd, nil)
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, filepath.Join(cwd, "link"), "relative"} {
		if _, err := p.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: "s", Path: path}); err == nil {
			t.Fatal("read outside workspace", path)
		}
		if _, err := p.WriteTextFile(ctx, acp.WriteTextFileRequest{SessionId: "s", Path: path, Content: "changed"}); err == nil {
			t.Fatal("wrote outside workspace", path)
		}
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "private" {
		t.Fatal("outside file modified")
	}
}

func TestTailBuffer(t *testing.T) {
	for _, limit := range []int{0, 1, 4, 1024} {
		b := &tailBuffer{limit: limit}
		for _, chunk := range []string{"a", "🌿", "last"} {
			_, _ = b.Write([]byte(chunk))
		}
		out, truncated := b.snapshot()
		if !utf8.ValidString(out) || len(out) > limit {
			t.Fatal(limit, out)
		}
		if limit < 9 && !truncated {
			t.Fatal("lost truncation flag")
		}
	}
}

func TestProcessAndTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := Start(ctx, testutil.Agent(t), t.TempDir(), Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !p.Info.AgentCapabilities.LoadSession || !p.Alive() {
		t.Fatal("not initialized")
	}
	if _, err := p.CreateTerminal(ctx, acp.CreateTerminalRequest{SessionId: "s", Command: "true"}); err == nil {
		t.Fatal("terminal created for an unknown session")
	}
	p.OpenSession("s", t.TempDir(), nil)
	limit := 6
	r, err := p.CreateTerminal(ctx, acp.CreateTerminalRequest{SessionId: "s", Command: "sh", Args: []string{"-c", "printf 'first-last'"}, OutputByteLimit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	exit, err := p.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{SessionId: "s", TerminalId: r.TerminalId})
	if err != nil || exit.ExitCode == nil || *exit.ExitCode != 0 {
		t.Fatal(exit, err)
	}
	out, err := p.TerminalOutput(ctx, acp.TerminalOutputRequest{SessionId: "s", TerminalId: r.TerminalId})
	if err != nil || out.Output != "t-last" || !out.Truncated {
		t.Fatal(out, err)
	}
	if _, err := p.TerminalOutput(ctx, acp.TerminalOutputRequest{SessionId: "other", TerminalId: r.TerminalId}); err == nil {
		t.Fatal("cross-session terminal access")
	}
	if _, err := p.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{SessionId: "s", TerminalId: r.TerminalId}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.TerminalOutput(ctx, acp.TerminalOutputRequest{SessionId: "s", TerminalId: r.TerminalId}); err == nil {
		t.Fatal("released terminal remains")
	}
	r, err = p.CreateTerminal(ctx, acp.CreateTerminalRequest{SessionId: "s", Command: "sh", Args: []string{"-c", "sleep 60 & wait"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.KillTerminal(ctx, acp.KillTerminalRequest{SessionId: "s", TerminalId: r.TerminalId}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{SessionId: "s", TerminalId: r.TerminalId}); err != nil {
		t.Fatal(err)
	}
	p.Close()
	if p.Alive() {
		t.Fatal("process still alive")
	}
}

// A missing executable must stay an error, not trigger adapter discovery or
// runtime selection. pi-acp specifically regresses the removed special case.
func TestMissingCommandDoesNotSearchLocalTools(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, ".tools", "node_modules", "pi-acp", "dist", "index.js")
	if err := os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("PATH", t.TempDir())
	for _, command := range []string{"pi-acp", "another-acp-agent"} {
		p, err := Start(context.Background(), command, root, Hooks{})
		if p != nil {
			p.Close()
			t.Fatalf("unexpectedly launched %s", command)
		}
		if err == nil || !strings.HasPrefix(err.Error(), "start agent:") || !strings.Contains(err.Error(), command) {
			t.Fatalf("missing %s should report its own startup error, got %v", command, err)
		}
	}
}

func TestStartupFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := Start(ctx, `sh -c 'printf "bad agent" >&2; sleep 30'`, t.TempDir(), Hooks{})
	if err == nil || !strings.Contains(err.Error(), "bad agent") {
		t.Fatal(err)
	}
}
