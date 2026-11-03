package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

// TestProtocolBlockedPipeAgent is a fully offline agent that initializes a session, then stops reading stdin.
func TestProtocolBlockedPipeAgent(t *testing.T) {
	if os.Args[len(os.Args)-1] != "mu-protocol-blocked-stdin" {
		t.Skip("subprocess helper")
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var m struct {
			ID     json.RawMessage
			Method string
		}
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			os.Exit(2)
		}
		switch m.Method {
		case "initialize":
			fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"list":{},"resume":{},"close":{},"fork":{}}}}}`+"\n", m.ID)
		case "session/new":
			fmt.Fprintf(os.Stdout, `{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s"}}`+"\n", m.ID)
			time.Sleep(time.Hour) // Killed by the parent test's deferred Process.Close.
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestProtocolStopBlockedPipe(t *testing.T) {
	a := openTest(t, t.TempDir())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' -test.run=^TestProtocolBlockedPipeAgent$ -- mu-protocol-blocked-stdin"
	id := createTest(t, a, command)
	connected(t, a, id)
	a.mu.Lock()
	p := a.runtimes[id].conn.process
	a.mu.Unlock()
	defer p.Close() // Rescue blocked writes if the test fails.
	if err := a.Send(id, strings.Repeat("x", 2*1024*1024)); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return v.Status == "Working…" })
	// Wait until the prompt is actually blocked in a pipe write. Stopping
	// earlier could let the small cancellation frame reach the pipe first.
	deadline := time.Now().Add(5 * time.Second)
	for {
		stack := make([]byte, 2*1024*1024)
		n := goruntime.Stack(stack, true)
		s := string(stack[:n])
		if strings.Contains(s, "internal/poll.(*FD).Write") && strings.Contains(s, "(*ioConn).Write") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt never blocked in pipe write")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopped := time.Now()
	a.Stop(id)
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if elapsed := time.Since(stopped); elapsed > 4*time.Second {
		t.Fatalf("Stop exceeded its deadline: %s", elapsed)
	}
	if v.Connected || lastText(v) != "Stopped. The response may be incomplete." {
		t.Fatalf("blocked agent was not terminated: %+v", v)
	}
}

func protocolRuntime(t *testing.T) (*App, string, *runtime) {
	t.Helper()
	a := openTest(t, t.TempDir())
	id := createTest(t, a, "unused-agent")
	a.mu.Lock()
	r := a.runtimeLocked(id)
	r.session, r.acceptUpdates = "s", true
	a.mu.Unlock()
	return a, id, r
}

func TestProtocolMessageIDs(t *testing.T) {
	a, id, r := protocolRuntime(t)
	for _, part := range []struct{ id, text string }{{"msg1", "First."}, {"msg2", "Second."}} {
		messageID := part.id
		a.update(id, r, acp.SessionNotification{SessionID: "s", Update: withMessageID(acp.AgentMessageChunkSessionUpdate(acp.TextContentBlock(part.text)), messageID)})
	}
	got := findThread(t, a, id).Entries
	if len(got) != 2 {
		t.Fatalf("two different message IDs should be two messages; got %+v", got)
	}
}

func TestProtocolToolRawOutputPatch(t *testing.T) {
	a, id, r := protocolRuntime(t)
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: toolCallUpdate("tool", "Read", acp.ContentToolCallContent(acp.TextContentBlock("Keep this content")))})
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: toolPatchUpdate("tool", func(u *acp.SessionUpdate) { u.RawOutput = map[string]any{"done": true} })})
	if got := lastText(findThread(t, a, id)); got != "Keep this content\n" {
		t.Fatalf("rawOutput-only patch replaced content: %q", got)
	}
}

func TestProtocolToolContentReplacement(t *testing.T) {
	a, id, r := protocolRuntime(t)
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: toolCallUpdate("tool", "Run", acp.TerminalToolCallContent("term"))})
	a.update(id, r, acp.SessionNotification{SessionID: "s", Update: toolPatchUpdate("tool", func(u *acp.SessionUpdate) {
		u.Content = &[]acp.ToolCallContent{acp.ContentToolCallContent(acp.TextContentBlock("Replacement summary"))}
	})})
	if got := lastText(findThread(t, a, id)); got != "Replacement summary\n" {
		t.Fatalf("content update did not replace the terminal reference: %q", got)
	}
}

func TestProtocolSetupResponseWinsReplayConfig(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	id := createTest(t, a, testutil.Agent(t)+" session-ui")
	sendDone(t, a, id, "updates") // Historical config_option_update sets model=deep.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	v := connected(t, a, id)
	// A fresh fixture process sends historical deep during load, then returns
	// current configOptions with model=fast in the load response.
	if got := v.Session.Options[0].CurrentValue; got != "fast" {
		t.Fatalf("pre-response replay overwrote current load response: model=%s", got)
	}
}

func TestProtocolEchoUsesMessageIdentity(t *testing.T) {
	a, id, r := protocolRuntime(t)
	a.mu.Lock()
	local := state.Entry{ID: state.ID(), Role: "user", Text: "hello"}
	r.Entries = append(r.Entries, local)
	r.job = &job{}
	r.echo = promptEcho{target: "hello", local: local.ID}
	a.mu.Unlock()
	t.Cleanup(func() { a.mu.Lock(); r.job = nil; a.mu.Unlock() })
	chunk := func(messageID, text string) {
		a.update(id, r, acp.SessionNotification{SessionID: "s", Update: withMessageID(acp.UserMessageChunkSessionUpdate(acp.TextContentBlock(text)), messageID)})
	}
	chunk("u1", "hel")
	chunk("u1", "lo")
	if got := findThread(t, a, id).Entries; len(got) != 1 || got[0].Text != "hello" || got[0].MessageID != "u1" {
		t.Fatalf("echo was not matched to the local prompt: %+v", got)
	}
	// Another user message with the same text is not the echo.
	chunk("u2", "hello")
	if got := findThread(t, a, id).Entries; len(got) != 2 || got[1].MessageID != "u2" {
		t.Fatalf("separate user message was treated as an echo: %+v", got)
	}
	// The agent's account of the prompt message is authoritative.
	chunk("u1", " (expanded)")
	if got := findThread(t, a, id).Entries; got[0].Text != "hello (expanded)" {
		t.Fatalf("agent revision of the prompt was not applied: %+v", got)
	}
}

// Test constructors for the SDK's flattened session update unions.

func toolCallUpdate(id, title string, content ...acp.ToolCallContent) acp.SessionUpdate {
	u := acp.ToolCallSessionUpdate(acp.ToolCallId(id), title)
	if content != nil {
		u.Content = content
	}
	return u
}

// toolPatchUpdate is a tool_call_update; set gives it the fields it supplies.
func toolPatchUpdate(id string, set func(*acp.SessionUpdate)) acp.SessionUpdate {
	u := acp.ToolCallUpdateSessionUpdate(acp.ToolCallId(id))
	set(&u)
	return u
}

func withMessageID(u acp.SessionUpdate, id string) acp.SessionUpdate {
	m := acp.MessageId(id)
	u.MessageID = &m
	return u
}
