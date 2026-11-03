package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestTailBuffer(t *testing.T) {
	for _, limit := range []int{0, 1, 4, 1024} {
		b := &tailBuffer{limit: limit}
		for _, chunk := range []string{"a", "🌿", "last"} {
			_, _ = b.Write([]byte(chunk))
		}
		out := b.snapshot()
		if !utf8.ValidString(out) || len(out) > limit {
			t.Fatal(limit, out)
		}
	}
}

func TestProcessLifecycle(t *testing.T) {
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
	// The test agent rejects initialization that advertises client file
	// system or terminal methods (TestClientMethodsAreUnknown covers requests).
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

// Close gives the agent time to exit after its input closes, then asks it to
// terminate, before killing it.
func TestCloseIsGraceful(t *testing.T) {
	command := testutil.Agent(t)
	logged := func(method string) bool {
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(command), "fixture-sessions", "calls.jsonl"))
		return strings.Contains(string(b), `"method":"`+method+`"`)
	}
	for _, c := range []struct{ arg, method string }{{"slow-exit", "exited"}, {"ignore-eof", "terminated"}} {
		p, err := Start(context.Background(), command+" "+c.arg, t.TempDir(), Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		p.Close()
		if !logged(c.method) {
			t.Fatalf("%s: agent was killed before it could exit", c.arg)
		}
		if elapsed := time.Since(started); elapsed > exitGrace+terminateGrace+time.Second {
			t.Fatalf("%s: close took %s", c.arg, elapsed)
		}
	}
}
