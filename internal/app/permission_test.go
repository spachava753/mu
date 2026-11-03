package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

// permissionAnswer returns Mu's response to the fixture request whose ID has
// the prefix, once the fixture has logged it.
func permissionAnswer(t *testing.T, command, prefix string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(command), "fixture-sessions", "calls.jsonl"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(b))
		scanner.Buffer(nil, 16*1024*1024)
		for scanner.Scan() {
			var m struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(scanner.Bytes(), &m) == nil && m.Method == "" && strings.HasPrefix(m.ID, prefix) {
				return string(m.Result)
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no answer to", prefix)
	return ""
}

func TestPermissionDescribesKnownToolCall(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if err := a.Send(id, "permission-ref"); err != nil {
		t.Fatal(err)
	}
	// The request names only the tool call ID; the card uses its projection.
	v := await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) == 1 && v.Permissions[0].Tool.Title != "" })
	tool := v.Permissions[0].Tool
	if tool.Title != "Edit notes" || tool.Kind != "edit" || !strings.HasPrefix(tool.Text, "/workspace/notes.txt:3\n") || !strings.Contains(tool.Text, "After:\nnew") || !strings.Contains(tool.Details, `"title": "Edit notes"`) {
		t.Fatalf("permission does not describe the known tool call: %+v", tool)
	}
	a.Answer(id, v.Permissions[0].ID, "always")
	v = await(t, a, id, func(v ThreadView) bool { return !v.Running })
	if !strings.Contains(lastText(v), `"optionId":"always"`) {
		t.Fatal(lastText(v))
	}
}

func TestPermissionsInArrivalOrder(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if err := a.Send(id, "permissions-two"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) == 2 })
	for range 20 {
		p := findThread(t, a, id).Permissions
		if p[0].Tool.Title != "First" || p[1].Tool.Title != "Second" || p[0].Tool.Kind != "execute" {
			t.Fatalf("permissions out of arrival order: %+v", p)
		}
	}
	p := findThread(t, a, id).Permissions
	a.Answer(id, p[1].ID, "no")
	a.Answer(id, p[0].ID, "yes")
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running })
	// Replies are written concurrently, so the agent may see them in any order.
	answers := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(lastText(v)), "\n") {
		n, answer, _ := strings.Cut(strings.TrimPrefix(line, "Answer "), ":")
		answers[n] = answer
	}
	if len(v.Permissions) != 0 || len(answers) != 2 || !strings.Contains(answers["1"], `"optionId":"yes"`) || !strings.Contains(answers["2"], `"optionId":"no"`) {
		t.Fatalf("answers were misrouted: %q", lastText(v))
	}
}

func TestPermissionOutsideTurn(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	// A saved agent's process outlives the reload, so the agent receives the
	// cancellation; a terminated process would not need it.
	agentID, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: command}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.CreateWithAgent(agentID, "")
	if err != nil {
		t.Fatal(err)
	}
	sendDone(t, a, id, "permission-idle")
	v := await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) == 1 })
	if v.Running || v.Permissions[0].Tool.Title != "Background task" {
		t.Fatalf("idle permission request: %+v", v)
	}
	// Ending the turn did not answer it; the user does.
	time.Sleep(50 * time.Millisecond)
	if v = findThread(t, a, id); len(v.Permissions) != 1 {
		t.Fatal("idle permission request was dropped")
	}
	a.Answer(id, v.Permissions[0].ID, "yes")
	v = await(t, a, id, func(v ThreadView) bool { return strings.Contains(lastText(v), `"optionId":"yes"`) })
	if len(v.Permissions) != 0 {
		t.Fatal("answered permission still pending")
	}

	// Detaching the session answers a pending request as cancelled.
	sendDone(t, a, id, "permission-idle")
	await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) == 1 })
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	if answer := permissionAnswer(t, command, "idle:"+v.SessionID+":2"); !strings.Contains(answer, `"cancelled"`) || strings.Contains(answer, "optionId") {
		t.Fatalf("detached session's permission answered %s", answer)
	}
	if v = await(t, a, id, func(v ThreadView) bool { return !v.Configuring }); len(v.Permissions) != 0 {
		t.Fatal("cancelled permission still displayed")
	}
}

func TestPermissionDuringLoadDescribesBufferedToolCall(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" load-permission")
	sendDone(t, a, id, "hello")
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	// The load waits for the answer, so its updates are still buffered. The
	// SDK may deliver the request before the tool_call notification sent first.
	v := await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) == 1 && v.Permissions[0].Tool.Title != "" })
	if !v.Configuring || v.Permissions[0].Tool.Title != "Resume edit" || v.Permissions[0].Tool.Kind != "edit" {
		t.Fatalf("load-time permission lacks its buffered tool call: configuring=%v %+v", v.Configuring, v.Permissions[0].Tool)
	}
	a.Answer(id, v.Permissions[0].ID, "yes")
	v = await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
	if !v.Connected || v.SessionError != "" || len(v.Entries) != 3 || v.Entries[1].Text != "Echo: hello" || v.Entries[2].Title != "Resume edit" {
		t.Fatalf("load did not complete after the answer: %+v", v)
	}
}

// Buffered setup updates refresh a displayed permission card without
// committing the replay.
func TestBufferedToolUpdateRefreshesPermission(t *testing.T) {
	a, id, r := protocolRuntime(t)
	a.mu.Lock()
	r.initializing = true
	r.pending["p"] = &pending{Permission: Permission{ID: "p", Request: acp.RequestPermissionRequest{SessionID: "s", ToolCall: acp.ToolCallUpdate{ToolCallID: "tool"}}}}
	a.mu.Unlock()
	drain := func() {
		select {
		case <-a.Changes():
		default:
		}
	}
	deliver := func(update acp.SessionUpdate) {
		t.Helper()
		drain()
		a.mu.Lock()
		a.receiveLocked(id, r, r.epoch, acp.SessionNotification{SessionID: "s", Update: update})
		a.mu.Unlock()
		select {
		case <-a.Changes():
		default:
			t.Fatal("buffered update did not signal a change")
		}
	}
	run := toolCallUpdate("tool", "Run")
	kind := acp.ToolKindExecute
	run.Kind = &kind
	deliver(run)
	if v := findThread(t, a, id); v.Permissions[0].Tool.Title != "Run" || len(v.Entries) != 0 {
		t.Fatalf("buffered tool call: %+v", v)
	}
	title := "Run tests"
	deliver(toolPatchUpdate("tool", func(u *acp.SessionUpdate) { u.Title = &title }))
	if v := findThread(t, a, id); v.Permissions[0].Tool.Title != title || v.Permissions[0].Tool.Kind != "execute" || len(v.Entries) != 0 {
		t.Fatalf("buffered tool call update: %+v", v)
	}
}
