package piacp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
)

// TestRealPi runs the adapter against a real Pi executable, whose only provider is
// a fake model server in this test, so that no real model is called. It needs
// MU_TEST_REAL_PI set to the executable. See .plan/pi-support/rpc-probe/run-go.sh
// for running it with no network and a cleared environment.
func TestRealPi(t *testing.T) {
	pi := os.Getenv("MU_TEST_REAL_PI")
	if pi == "" {
		t.Skip("set MU_TEST_REAL_PI to a Pi executable")
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		requests.Add(1)
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		last := body.Messages[len(body.Messages)-1]
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta map[string]any, finish any) {
			data, _ := json.Marshal(map[string]any{"id": "fake", "object": "chat.completion.chunk", "created": 1, "model": "fake", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
		text := fmt.Sprint(last.Content)
		switch {
		case last.Role != "tool" && strings.Contains(text, "TOOLCALL"):
			chunk(map[string]any{"role": "assistant", "content": "Running. "}, nil)
			arguments, _ := json.Marshal(map[string]any{"command": "echo tool-output"})
			chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_real", "type": "function", "function": map[string]any{"name": "bash", "arguments": string(arguments)}}}}, nil)
			chunk(map[string]any{}, "tool_calls")
		case strings.Contains(text, "SLOWLY"):
			chunk(map[string]any{"role": "assistant", "content": "slow "}, nil)
			for range 400 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
				chunk(map[string]any{"content": "word "}, nil)
			}
			chunk(map[string]any{}, "stop")
		default:
			chunk(map[string]any{"role": "assistant", "content": "Hello "}, nil)
			chunk(map[string]any{"content": "from the fake model."}, nil)
			chunk(map[string]any{}, "stop")
		}
		data, _ := json.Marshal(map[string]any{"id": "fake", "object": "chat.completion.chunk", "created": 1, "model": "fake", "choices": []any{}, "usage": map[string]any{"prompt_tokens": 40, "completion_tokens": 8, "total_tokens": 48}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}))
	defer server.Close()

	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fake": map[string]any{
		"baseUrl": server.URL + "/v1", "api": "openai-completions", "apiKey": "not-a-real-key",
		"models": []any{
			map[string]any{"id": "basic", "name": "Fake Basic", "reasoning": false, "input": []string{"text"}, "contextWindow": 100000, "maxTokens": 4096, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}},
			map[string]any{"id": "deep", "name": "Fake Deep", "reasoning": true, "input": []string{"text"}, "contextWindow": 200000, "maxTokens": 4096, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}},
		},
	}}})
	settings, _ := json.Marshal(map[string]any{"defaultProvider": "fake", "defaultModel": "basic"})
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PI_SKIP_VERSION_CHECK", "1")
	t.Setenv("PI_TELEMETRY", "0")

	e := newEnv(t)
	realPi := &realEnv{env: e, pi: pi}
	c := realPi.connect()
	cwd := e.folder("a")
	ctx, cancel := c.ctx()
	defer cancel()

	created, err := c.p.Conn.SessionNew(ctx, &acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(c.p.Error(err))
	}
	id := created.SessionID
	if got := optionNames(*created.ConfigOptions); len(got) != 1 || got[0] != "model" {
		t.Fatalf("options %v", got)
	}
	if !strings.Contains(fmt.Sprint(created.ConfigOptions), "fake/basic") {
		t.Fatalf("options %+v", *created.ConfigOptions)
	}
	changed, err := c.p.Conn.SessionSetConfigOption(ctx, &acp.SetSessionConfigOptionRequest{SessionID: id, ConfigID: "model", Value: "fake/deep"})
	if err != nil {
		t.Fatal(err)
	}
	if got := optionNames(changed.ConfigOptions); len(got) != 2 {
		t.Fatalf("options after changing model %v", got)
	}

	c.reset()
	response, err := c.prompt(id, "hello")
	if err != nil {
		t.Fatal(c.p.Error(err))
	}
	if response.StopReason != acp.StopReasonEndTurn || response.Usage == nil {
		t.Fatalf("response %+v", response)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); got != "Hello from the fake model." {
		t.Fatalf("reply %q", got)
	}

	c.reset()
	if _, err := c.prompt(id, "please TOOLCALL now"); err != nil {
		t.Fatal(c.p.Error(err))
	}
	completed := false
	var output string
	for _, u := range c.received(id) {
		if u.ToolCallID == "call_real" && u.Status != nil && *u.Status == acp.ToolCallStatusCompleted {
			completed = true
			if blocks := toolBlocks(u); len(blocks) > 0 {
				output = blocks[0].Content.Text
			}
		}
	}
	if !completed || !strings.Contains(output, "tool-output") {
		t.Fatalf("tool call: completed %v, output %q", completed, output)
	}

	// Cancelling, with the real abort.
	done := make(chan *acp.PromptResponse, 1)
	c.reset()
	go func() {
		response, err := c.prompt(id, "answer SLOWLY please")
		if err != nil {
			t.Error(err)
		}
		done <- response
	}()
	eventually(t, "streaming", func() bool {
		return strings.HasPrefix(c.text(id, acp.SessionUpdateTypeAgentMessageChunk), "slow")
	})
	if err := c.p.Conn.SessionCancel(ctx, &acp.CancelNotification{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response == nil || response.StopReason != acp.StopReasonCancelled {
			t.Fatalf("cancelled turn gave %+v", response)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancel did not end the turn")
	}

	// Listing, from Pi's real files, including a session never sent to.
	unused := c.newSession(cwd)
	listed := map[acp.SessionId]acp.SessionInfo{}
	for _, s := range c.list(nil) {
		listed[s.SessionID] = s
	}
	if s, ok := listed[id]; !ok || s.Title == nil || *s.Title != "hello" || s.Cwd != cwd {
		t.Fatalf("listed %+v", listed)
	}
	if _, ok := listed[unused]; !ok {
		t.Fatalf("unused session not listed: %+v", listed)
	}

	// Fork leaves the source working.
	forked, err := c.p.Conn.SessionFork(ctx, &acp.ForkSessionRequest{SessionID: id, Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(c.p.Error(err))
	}
	if err := c.p.CloseSession(string(forked.SessionID), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	c.reset()
	if err := c.load(forked.SessionID, cwd); err != nil {
		t.Fatal(c.p.Error(err))
	}
	if got := c.text(forked.SessionID, acp.SessionUpdateTypeAgentMessageChunk); !strings.Contains(got, "Hello from the fake model.") {
		t.Fatalf("fork's history %q", got)
	}
	if _, err := c.prompt(forked.SessionID, "on the fork"); err != nil {
		t.Fatal(c.p.Error(err))
	}
	if _, err := c.prompt(id, "on the source"); err != nil {
		t.Fatal(c.p.Error(err))
	}

	// Across a restart: history replays with the model it had, an unused session
	// reopens, and a session with no marker and no file doesn't appear from nowhere.
	c.p.Close()
	c = realPi.connect()
	c.reset()
	if err := c.load(id, cwd); err != nil {
		t.Fatal(c.p.Error(err))
	}
	if got := c.text(id, acp.SessionUpdateTypeUserMessageChunk); !strings.Contains(got, "hello") {
		t.Fatalf("replayed user text %q", got)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); !strings.Contains(got, "Hello from the fake model.") {
		t.Fatalf("replayed reply %q", got)
	}
	if err := c.load(unused, cwd); err != nil {
		t.Fatalf("unused session: %v", c.p.Error(err))
	}
	if err := c.load("00000000-0000-4000-8000-000000000000", cwd); code(err) != -32002 {
		t.Fatalf("unknown session gave: %v", err)
	}
	// /compact doesn't reach the model as text.
	before := requests.Load()
	if _, err := c.prompt(id, "/compact"); err != nil && !strings.Contains(err.Error(), "ompact") {
		t.Fatalf("compact gave: %v", err)
	}
	if requests.Load() != before {
		t.Log("compacting asked the model for a summary, as it may")
	}
	c.p.Close()
	if requests.Load() == 0 {
		t.Fatal("the fake model was never called")
	}
}

// realEnv connects the adapter to a real Pi instead of the fake.
type realEnv struct {
	*env
	pi string
}

func (e *realEnv) connect() *client { return e.env.connectTo(e.pi) }
