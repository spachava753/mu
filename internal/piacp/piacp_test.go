package piacp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/acp-sdk/jsonrpc"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/piacp"
)

var (
	fakePiOnce sync.Once
	fakePiPath string
	fakePiErr  error
)

// fakePi builds the deterministic stand-in for Pi, once per test run.
func fakePi(t *testing.T) string {
	t.Helper()
	fakePiOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakepi")
		if err != nil {
			fakePiErr = err
			return
		}
		fakePiPath = filepath.Join(dir, "pi")
		if runtime.GOOS == "windows" {
			fakePiPath += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", fakePiPath, "./testdata/fakepi")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakePiErr = fmt.Errorf("build fake pi: %v\n%s", err, out)
		}
	})
	if fakePiErr != nil {
		t.Fatal(fakePiErr)
	}
	return fakePiPath
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fakePiPath != "" {
		_ = os.RemoveAll(filepath.Dir(fakePiPath))
	}
	os.Exit(code)
}

// env is one test's isolated Pi: where sessions go, and a log of what it was told.
type env struct {
	t        *testing.T
	sessions string
	state    string
	log      string
	work     string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, sessions: t.TempDir(), state: t.TempDir(), log: filepath.Join(t.TempDir(), "pi.log"), work: t.TempDir()}
	t.Setenv("FAKEPI_SESSIONS", e.sessions)
	t.Setenv("FAKEPI_LOG", e.log)
	return e
}

func (e *env) folder(name string) string {
	e.t.Helper()
	dir := filepath.Join(e.work, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dir
}

// server runs the adapter against a Pi executable.
func (e *env) server(pi string) agent.Server {
	return func(ctx context.Context, link agent.Link) error {
		return piacp.Serve(ctx, link.Transport, piacp.Config{Executable: func() string { return pi }, WorkDir: link.Cwd, StateDir: e.state, Diag: link.Diag})
	}
}

// calls lists the commands the fake Pi received, in order.
func (e *env) calls() []string {
	e.t.Helper()
	data, err := os.ReadFile(e.log)
	if err != nil {
		return nil
	}
	var out []string
	for line := range strings.Lines(string(data)) {
		var entry struct {
			Command string `json:"command"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Command != "" {
			out = append(out, strings.TrimSpace(entry.Command+" "+entry.Message))
		}
	}
	return out
}

// pids lists the Pi processes started.
func (e *env) pids() []int {
	e.t.Helper()
	data, err := os.ReadFile(e.log)
	if err != nil {
		return nil
	}
	var out []int
	for line := range strings.Lines(string(data)) {
		var entry struct {
			Args []string `json:"args"`
			Pid  int      `json:"pid"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Args != nil {
			out = append(out, entry.Pid)
		}
	}
	return out
}

// client is Mu's real ACP client connected to the adapter.
type client struct {
	t *testing.T
	p *agent.Process

	mu      sync.Mutex
	updates []acp.SessionNotification
	answer  func(acp.RequestPermissionRequest) acp.RequestPermissionResponse
	asked   []acp.RequestPermissionRequest
}

func (e *env) connect() *client {
	e.t.Helper()
	return e.connectTo(fakePi(e.t))
}

func (e *env) connectTo(pi string) *client {
	e.t.Helper()
	c := &client{t: e.t}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := agent.Serve(ctx, e.server(pi), e.work, agent.Hooks{
		Update: func(n acp.SessionNotification) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.updates = append(c.updates, n)
		},
		Permission: func(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			c.mu.Lock()
			c.asked = append(c.asked, req)
			answer := c.answer
			c.mu.Unlock()
			if answer == nil {
				return agent.CancelledPermission(), nil
			}
			return answer(req), nil
		},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	c.p = p
	e.t.Cleanup(p.Close)
	return c
}

func (c *client) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func (c *client) newSession(cwd string) acp.SessionId {
	c.t.Helper()
	ctx, cancel := c.ctx()
	defer cancel()
	response, err := c.p.Conn.SessionNew(ctx, &acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		c.t.Fatal(c.p.Error(err))
	}
	return response.SessionID
}

func (c *client) prompt(id acp.SessionId, text string) (*acp.PromptResponse, error) {
	ctx, cancel := c.ctx()
	defer cancel()
	return c.p.Conn.SessionPrompt(ctx, &acp.PromptRequest{SessionID: id, Prompt: []acp.ContentBlock{{Type: acp.ContentBlockTypeText, Text: text}}})
}

func (c *client) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = nil
}

func (c *client) received(id acp.SessionId) []acp.SessionUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []acp.SessionUpdate
	for _, n := range c.updates {
		if n.SessionID == id {
			out = append(out, n.Update)
		}
	}
	return out
}

func (c *client) text(id acp.SessionId, kind acp.SessionUpdateType) string {
	var out strings.Builder
	for _, u := range c.received(id) {
		if u.SessionUpdate == kind {
			if b, ok := u.Content.(acp.ContentBlock); ok {
				out.WriteString(b.Text)
			}
		}
	}
	return out.String()
}

func code(err error) int64 {
	var response *jsonrpc.Error
	if errors.As(err, &response) {
		return int64(response.Code)
	}
	return 0
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestInitializeReportsAMissingOrOldPi(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := agent.Serve(ctx, e.server(filepath.Join(e.work, "nothing-here")), e.work, agent.Hooks{})
	if err == nil || !strings.Contains(err.Error(), "Pi wasn't found") || !strings.Contains(err.Error(), "https://pi.dev") {
		t.Fatalf("missing Pi gave: %v", err)
	}
	t.Setenv("FAKEPI_VERSION", "0.9.2")
	_, err = agent.Serve(ctx, e.server(fakePi(t)), e.work, agent.Hooks{})
	if err == nil || !strings.Contains(err.Error(), "0.9.2") || !strings.Contains(err.Error(), piacp.MinimumVersion) {
		t.Fatalf("old Pi gave: %v", err)
	}
}

// Mu's own check of the capabilities it requires is part of starting an agent.
func TestCapabilitiesSatisfyMu(t *testing.T) {
	c := newEnv(t).connect()
	if !c.p.SessionTitles() {
		t.Fatal("titles not advertised")
	}
	if c.p.AdditionalDirectories() {
		t.Fatal("Pi has no additional directories, so they must not be advertised")
	}
	if c.p.Info.AgentInfo == nil || c.p.Info.AgentInfo.Version != "1.5.0" {
		t.Fatalf("agent info: %+v", c.p.Info.AgentInfo)
	}
}

func TestPromptStreamsInOrder(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	c.reset()
	response, err := c.prompt(id, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason %q", response.StopReason)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 48 {
		t.Fatalf("usage %+v", response.Usage)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); got != "Hello from Pi." {
		t.Fatalf("reply %q", got)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentThoughtChunk); got != "Considering." {
		t.Fatalf("thought %q", got)
	}
	// The system message and the user's own message aren't echoed.
	if got := c.text(id, acp.SessionUpdateTypeUserMessageChunk); got != "" {
		t.Fatalf("echoed %q", got)
	}
	updates := c.received(id)
	last := updates[len(updates)-1]
	if last.SessionUpdate != acp.SessionUpdateTypeUsageUpdate || last.Used != 48 || last.Size != 100000 || last.Cost == nil || last.Cost.Currency != "USD" {
		t.Fatalf("usage update should come last and before the response: %+v", last)
	}
}

func TestToolCallsAreProjected(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	cwd := e.folder("a")
	id := c.newSession(cwd)
	c.reset()
	if _, err := c.prompt(id, "use-a-tool"); err != nil {
		t.Fatal(err)
	}
	var statuses []acp.ToolCallStatus
	var title string
	var content []string
	sawInput := false
	for _, u := range c.received(id) {
		if u.ToolCallID != "call-1" {
			continue
		}
		if u.Status != nil {
			statuses = append(statuses, *u.Status)
		}
		if u.Title != nil {
			title = *u.Title
		}
		if u.RawInput != nil {
			sawInput = true
		}
		if blocks := toolBlocks(u); len(blocks) > 0 {
			content = append(content, blocks[0].Content.Text)
		}
	}
	if !slices.Equal(statuses, []acp.ToolCallStatus{acp.ToolCallStatusPending, acp.ToolCallStatusInProgress, acp.ToolCallStatusCompleted}) {
		t.Fatalf("statuses %v", statuses)
	}
	if title != "bash echo hi" {
		t.Fatalf("title %q", title)
	}
	if !sawInput {
		t.Fatal("tool input not reported")
	}
	if !slices.Equal(content, []string{"hi\n", "hi\nthere\n"}) {
		t.Fatalf("content %q", content)
	}
	// Text before and after the tool stay separate messages.
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); got != "Running. Done." {
		t.Fatalf("text %q", got)
	}
}

// toolBlocks reads a tool update's content, which the SDK decodes as a slice or a
// pointer to one depending on the update's type.
func toolBlocks(u acp.SessionUpdate) []acp.ToolCallContent {
	switch content := u.Content.(type) {
	case []acp.ToolCallContent:
		return content
	case *[]acp.ToolCallContent:
		return *content
	}
	return nil
}

func TestReasonsATurnEnds(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	// Pi accepts the prompt, and the failure is on the final message.
	if _, err := c.prompt(id, "fail-now"); err == nil || !strings.Contains(err.Error(), "the model refused") {
		t.Fatalf("failed turn gave: %v", err)
	}
	response, err := c.prompt(id, "too-long")
	if err != nil || response.StopReason != acp.StopReasonMaxTokens {
		t.Fatalf("too long gave %+v, %v", response, err)
	}
	c.reset()
	response, err = c.prompt(id, "retry-once")
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("a retry that recovers gave %+v, %v", response, err)
	}
	if !strings.Contains(c.text(id, acp.SessionUpdateTypeAgentThoughtChunk), "Retrying") {
		t.Fatal("a retry wasn't explained")
	}
	if c.p.Alive() != true {
		t.Fatal("process ended")
	}
}

func TestCancelEndsTheTurn(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	done := make(chan *acp.PromptResponse, 1)
	go func() {
		response, err := c.prompt(id, "run-slowly")
		if err != nil {
			t.Error(err)
		}
		done <- response
	}()
	eventually(t, "the turn to stream", func() bool { return c.text(id, acp.SessionUpdateTypeAgentMessageChunk) == "Working" })
	ctx, cancel := c.ctx()
	defer cancel()
	if err := c.p.Conn.SessionCancel(ctx, &acp.CancelNotification{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response != nil && response.StopReason != acp.StopReasonCancelled {
			t.Fatalf("stop reason %q", response.StopReason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not end the turn")
	}
	if response, err := c.prompt(id, "hello"); err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("session unusable after cancel: %+v, %v", response, err)
	}
}

func TestCloseCancelsARunningTurnAndStopsPi(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	other := c.newSession(e.folder("b"))
	done := make(chan *acp.PromptResponse, 1)
	go func() {
		response, err := c.prompt(id, "run-slowly")
		if err != nil {
			t.Error(err)
		}
		done <- response
	}()
	eventually(t, "the turn to stream", func() bool { return c.text(id, acp.SessionUpdateTypeAgentMessageChunk) == "Working" })
	if err := c.p.CloseSession(string(id), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response != nil && response.StopReason != acp.StopReasonCancelled {
			t.Fatalf("stop reason %q", response.StopReason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("close did not end the turn")
	}
	if _, err := c.prompt(id, "hello"); code(err) != -32002 {
		t.Fatalf("closed session gave: %v", err)
	}
	// Other sessions have their own process, and are unaffected.
	if _, err := c.prompt(other, "hello"); err != nil {
		t.Fatal(err)
	}
	// Closing twice is what Mu counts as already closed.
	if err := c.p.CloseSession(string(id), 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownStopsEveryPi(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	c.newSession(e.folder("a"))
	c.newSession(e.folder("b"))
	pids := e.pids()
	if len(pids) < 2 {
		t.Fatalf("expected a process per session, got %v", pids)
	}
	c.p.Close()
	for _, pid := range pids {
		if alive(pid) {
			t.Fatalf("Pi process %d outlived the adapter", pid)
		}
	}
}

func alive(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// A zombie has exited, and is its parent's to reap.
	fields := strings.Fields(string(data))
	return len(fields) > 2 && fields[2] != "Z"
}

func TestAProcessThatDiesEndsItsTurn(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	other := c.newSession(e.folder("b"))
	if _, err := c.prompt(id, "exit-now"); err == nil || !strings.Contains(err.Error(), "Pi exited") {
		t.Fatalf("dead Pi gave: %v", err)
	}
	if _, err := c.prompt(id, "hello"); code(err) != -32002 {
		t.Fatalf("a dead session gave: %v", err)
	}
	if _, err := c.prompt(other, "hello"); err != nil {
		t.Fatalf("another session was affected: %v", err)
	}
}

func (c *client) load(id acp.SessionId, cwd string) error {
	ctx, cancel := c.ctx()
	defer cancel()
	_, err := c.p.Conn.SessionLoad(ctx, &acp.LoadSessionRequest{SessionID: id, Cwd: cwd, McpServers: []acp.McpServer{}})
	return err
}

func TestLoadReplaysAcrossRestart(t *testing.T) {
	e := newEnv(t)
	cwd := e.folder("a")
	c := e.connect()
	id := c.newSession(cwd)
	if _, err := c.prompt(id, "use-a-tool"); err != nil {
		t.Fatal(err)
	}
	c.p.Close()

	c = e.connect()
	if err := c.load(id, cwd); err != nil {
		t.Fatal(err)
	}
	if got := c.text(id, acp.SessionUpdateTypeUserMessageChunk); got != "use-a-tool" {
		t.Fatalf("replayed user text %q", got)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); got != "Running. Done." {
		t.Fatalf("replayed reply %q", got)
	}
	var statuses []acp.ToolCallStatus
	for _, u := range c.received(id) {
		if u.ToolCallID == "call-1" && u.Status != nil {
			statuses = append(statuses, *u.Status)
		}
	}
	if !slices.Equal(statuses, []acp.ToolCallStatus{acp.ToolCallStatusPending, acp.ToolCallStatusCompleted}) {
		t.Fatalf("replayed tool statuses %v", statuses)
	}
	if _, err := c.prompt(id, "hello again"); err != nil {
		t.Fatal(err)
	}
}

// A thread nothing was ever sent in has no file, because Pi writes none until the
// first turn, but it still has to reopen. A session that did exist and was deleted
// must not be silently replaced by a new empty one.
func TestUnusedSessionsReopenAndDeletedOnesDont(t *testing.T) {
	e := newEnv(t)
	cwd := e.folder("a")
	c := e.connect()
	unused := c.newSession(cwd)
	used := c.newSession(cwd)
	if _, err := c.prompt(used, "hello"); err != nil {
		t.Fatal(err)
	}
	c.p.Close()

	matches, _ := filepath.Glob(filepath.Join(e.sessions, "*", "*_"+string(used)+".jsonl"))
	if len(matches) != 1 {
		t.Fatalf("session file: %v", matches)
	}
	if err := os.Remove(matches[0]); err != nil {
		t.Fatal(err)
	}

	c = e.connect()
	if err := c.load(unused, cwd); err != nil {
		t.Fatalf("an unused session didn't reopen: %v", err)
	}
	if err := c.load(used, cwd); code(err) != -32002 {
		t.Fatalf("a deleted session gave: %v", err)
	}
	if err := c.load("never-existed", cwd); code(err) != -32002 {
		t.Fatalf("an unknown session gave: %v", err)
	}
	if err := c.load("--inject", cwd); code(err) != -32002 {
		t.Fatalf("a session ID shaped like a flag gave: %v", err)
	}
	// An unused session in the wrong folder would be a new session there.
	if err := c.load(unused, e.folder("elsewhere")); err == nil {
		t.Fatal("a session reopened in the wrong folder")
	}
}

func TestFirstTurnForgetsTheMarker(t *testing.T) {
	e := newEnv(t)
	cwd := e.folder("a")
	c := e.connect()
	id := c.newSession(cwd)
	marker := filepath.Join(e.state, "unsent", string(id)+".json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no marker for a new session: %v", err)
	}
	if _, err := c.prompt(id, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker kept after the first turn: %v", err)
	}
}

func (c *client) list(cwd *string) []acp.SessionInfo {
	c.t.Helper()
	ctx, cancel := c.ctx()
	defer cancel()
	response, err := c.p.Conn.SessionList(ctx, &acp.ListSessionsRequest{Cwd: cwd})
	if err != nil {
		c.t.Fatal(c.p.Error(err))
	}
	return response.Sessions
}

func TestListFindsSessionsAcrossFolders(t *testing.T) {
	e := newEnv(t)
	a, b := e.folder("a"), e.folder("b")
	c := e.connect()
	first := c.newSession(a)
	if _, err := c.prompt(first, "the first question\nwith a second line"); err != nil {
		t.Fatal(err)
	}
	named := c.newSession(b)
	if _, err := c.prompt(named, "hello"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := c.ctx()
	defer cancel()
	if _, err := c.p.Conn.SessionSetTitle(ctx, &acp.SetSessionTitleRequest{SessionID: named, Title: "  Named later "}); err != nil {
		t.Fatal(err)
	}
	unused := c.newSession(b)

	byID := map[acp.SessionId]acp.SessionInfo{}
	for _, s := range c.list(nil) {
		byID[s.SessionID] = s
	}
	if len(byID) != 3 {
		t.Fatalf("listed %+v", byID)
	}
	if s := byID[first]; s.Title == nil || *s.Title != "the first question" || s.Cwd != a || s.UpdatedAt == nil {
		t.Fatalf("first %+v", s)
	}
	if s := byID[named]; s.Title == nil || *s.Title != "Named later" || s.Cwd != b {
		t.Fatalf("named %+v", s)
	}
	// Listed so that Mu doesn't mark it missing, with no title to show.
	if s := byID[unused]; s.Title != nil || s.Cwd != b {
		t.Fatalf("unused %+v", s)
	}
	if only := c.list(&a); len(only) != 1 || only[0].SessionID != first {
		t.Fatalf("filtered %+v", only)
	}
}

func TestListWithACustomSessionDirectory(t *testing.T) {
	t.Setenv("FAKEPI_FLAT", "1")
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	if _, err := c.prompt(id, "hello"); err != nil {
		t.Fatal(err)
	}
	if listed := c.list(nil); len(listed) != 1 || listed[0].SessionID != id {
		t.Fatalf("listed %+v", listed)
	}
	// Files the adapter can't read are skipped, not fatal.
	if err := os.WriteFile(filepath.Join(e.sessions, "garbage.jsonl"), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if listed := c.list(nil); len(listed) != 1 {
		t.Fatalf("listed %+v", listed)
	}
}

func TestForkLeavesTheSourceAlone(t *testing.T) {
	e := newEnv(t)
	cwd := e.folder("a")
	c := e.connect()
	id := c.newSession(cwd)
	if _, err := c.prompt(id, "hello"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := c.ctx()
	defer cancel()
	response, err := c.p.Conn.SessionFork(ctx, &acp.ForkSessionRequest{SessionID: id, Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	if response.SessionID == id {
		t.Fatal("fork has the source's ID")
	}
	// Mu closes the fork and loads it later on the same connection.
	if err := c.p.CloseSession(string(response.SessionID), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	c.reset()
	if err := c.load(response.SessionID, cwd); err != nil {
		t.Fatal(err)
	}
	if got := c.text(response.SessionID, acp.SessionUpdateTypeUserMessageChunk); got != "hello" {
		t.Fatalf("fork's history %q", got)
	}
	if _, err := c.prompt(response.SessionID, "on the fork"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.prompt(id, "on the source"); err != nil {
		t.Fatalf("source unusable after a fork: %v", err)
	}
}

func TestForkOfAnUnusedSessionIsANewSession(t *testing.T) {
	e := newEnv(t)
	cwd := e.folder("a")
	c := e.connect()
	id := c.newSession(cwd)
	ctx, cancel := c.ctx()
	defer cancel()
	response, err := c.p.Conn.SessionFork(ctx, &acp.ForkSessionRequest{SessionID: id, Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(c.p.Error(err))
	}
	if response.SessionID == id {
		t.Fatal("fork has the source's ID")
	}
	if _, err := c.prompt(response.SessionID, "hello"); err != nil {
		t.Fatal(err)
	}
}

func optionNames(options []acp.SessionConfigOption) []string {
	names := []string{}
	for _, o := range options {
		names = append(names, string(o.ID))
	}
	return names
}

func TestOptionsFollowWhatPiReports(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	ctx, cancel := c.ctx()
	defer cancel()
	created, err := c.p.Conn.SessionNew(ctx, &acp.NewSessionRequest{Cwd: e.folder("a"), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	// This model can't think, so it offers no thinking option.
	if got := optionNames(*created.ConfigOptions); !slices.Equal(got, []string{"model"}) {
		t.Fatalf("options %v", got)
	}
	set := func(option string, value any) (*acp.SetSessionConfigOptionResponse, error) {
		return c.p.Conn.SessionSetConfigOption(ctx, &acp.SetSessionConfigOptionRequest{SessionID: created.SessionID, ConfigID: acp.SessionConfigId(option), Value: value})
	}
	changed, err := set("model", "fake/deep")
	if err != nil {
		t.Fatal(err)
	}
	if got := optionNames(changed.ConfigOptions); !slices.Equal(got, []string{"model", "thinking"}) {
		t.Fatalf("options after changing model %v", got)
	}
	// Pi would accept this and quietly reset the level.
	if _, err := set("thinking", "nonsense"); code(err) != -32602 {
		t.Fatalf("invalid level gave: %v", err)
	}
	changed, err = set("thinking", "high")
	if err != nil {
		t.Fatal(err)
	}
	if got := changed.ConfigOptions[1].CurrentValue; fmt.Sprint(got) != "high" {
		t.Fatalf("thinking %v", got)
	}
	// A model that can't think resets the level, and the option goes away.
	changed, err = set("model", "fake/basic")
	if err != nil {
		t.Fatal(err)
	}
	if got := optionNames(changed.ConfigOptions); !slices.Equal(got, []string{"model"}) {
		t.Fatalf("options after clamping %v", got)
	}
	if _, err := set("model", "fake/none"); code(err) != -32602 || !strings.Contains(err.Error(), "Model not found") {
		t.Fatalf("unknown model gave: %v", err)
	}
	if _, err := set("mode", "x"); code(err) != -32602 {
		t.Fatalf("unknown option gave: %v", err)
	}
}

func TestSignedOutPi(t *testing.T) {
	t.Setenv("FAKEPI_NO_MODELS", "1")
	e := newEnv(t)
	c := e.connect()
	ctx, cancel := c.ctx()
	defer cancel()
	created, err := c.p.Conn.SessionNew(ctx, &acp.NewSessionRequest{Cwd: e.folder("a"), McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	// Pi calls its model "unknown" here, and that isn't an option to offer.
	if len(*created.ConfigOptions) != 0 {
		t.Fatalf("options %+v", *created.ConfigOptions)
	}
	_, err = c.prompt(created.SessionID, "hello")
	if code(err) != -32000 || !strings.Contains(err.Error(), "/login") {
		t.Fatalf("signed-out prompt gave: %v", err)
	}
	if _, err := c.prompt(created.SessionID, "hello"); code(err) != -32000 {
		t.Fatalf("the session should stay usable: %v", err)
	}
}

func TestCommandsIncludeCompact(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	var names []string
	for _, u := range c.received(id) {
		if u.SessionUpdate == acp.SessionUpdateTypeAvailableCommandsUpdate {
			for _, command := range u.AvailableCommands {
				names = append(names, command.Name)
			}
		}
	}
	if !slices.Equal(names, []string{"compact", "fix-tests"}) {
		t.Fatalf("commands %v", names)
	}
}

// Sent as a prompt, /compact would reach the model as plain text.
func TestCompactUsesItsOwnCommand(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	// Too small to compact is an answer, not a failure.
	if response, err := c.prompt(id, "/compact"); err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("compacting nothing gave: %+v, %v", response, err)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); !strings.Contains(got, "Nothing to compact") {
		t.Fatalf("compacting nothing said %q", got)
	}
	if _, err := c.prompt(id, "hello"); err != nil {
		t.Fatal(err)
	}
	c.reset()
	response, err := c.prompt(id, "/compact keep the decisions")
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("compact gave %+v, %v", response, err)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); !strings.Contains(got, "150000") || !strings.Contains(got, "32000") {
		t.Fatalf("compact message %q", got)
	}
	// Words that merely start the same way are prompts.
	if _, err := c.prompt(id, "/compaction explained"); err != nil {
		t.Fatal(err)
	}
	calls := e.calls()
	if !slices.Contains(calls, "compact") || slices.Contains(calls, "prompt /compact") {
		t.Fatalf("calls %q", calls)
	}
	if !slices.Contains(calls, "prompt /compaction explained") {
		t.Fatalf("calls %q", calls)
	}
}

// Pi's messages are what the model sees next, which after compacting is most of the
// way empty. Reopening a thread still shows the whole conversation, and only its
// active branch.
func TestReplayKeepsHistoryBeforeACompaction(t *testing.T) {
	e := newEnv(t)
	cwd := e.folder("a")
	c := e.connect()
	id := c.newSession(cwd)
	if _, err := c.prompt(id, "use-a-tool"); err != nil {
		t.Fatal(err)
	}
	c.reset()
	if _, err := c.prompt(id, "/compact"); err != nil {
		t.Fatal(err)
	}
	updates := c.received(id)
	last := updates[len(updates)-1]
	if last.SessionUpdate != acp.SessionUpdateTypeUsageUpdate || last.Used != 32000 || last.Size != 100000 || last.Cost == nil {
		t.Fatalf("usage after compacting should be Pi's estimate: %+v", last)
	}
	if _, err := c.prompt(id, "after compacting"); err != nil {
		t.Fatal(err)
	}
	c.p.Close()

	c = e.connect()
	if err := c.load(id, cwd); err != nil {
		t.Fatal(err)
	}
	if got := c.text(id, acp.SessionUpdateTypeUserMessageChunk); got != "use-a-toolafter compacting" {
		t.Fatalf("replayed user text %q", got)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentThoughtChunk); !strings.Contains(got, "Summary of earlier work.") {
		t.Fatalf("the compaction's summary wasn't replayed: %q", got)
	}
	tools := 0
	for _, u := range c.received(id) {
		if u.ToolCallID == "call-1" {
			tools++
		}
	}
	if tools == 0 {
		t.Fatal("tool calls from before the compaction weren't replayed")
	}
}

func TestToolGateBecomesAPermissionRequest(t *testing.T) {
	for _, allow := range []bool{true, false} {
		t.Run(fmt.Sprint(allow), func(t *testing.T) {
			e := newEnv(t)
			c := e.connect()
			c.answer = func(req acp.RequestPermissionRequest) acp.RequestPermissionResponse {
				option := "no"
				if allow {
					option = "yes"
				}
				return acp.RequestPermissionResponse{Outcome: acp.SelectedRequestPermissionOutcome(acp.PermissionOptionId(option))}
			}
			id := c.newSession(e.folder("a"))
			c.reset()
			if _, err := c.prompt(id, "gate-tool"); err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			asked := c.asked
			c.mu.Unlock()
			if len(asked) != 1 || asked[0].ToolCall.ToolCallID != "call-2" {
				t.Fatalf("asked %+v", asked)
			}
			kinds := []acp.PermissionOptionKind{}
			for _, o := range asked[0].Options {
				kinds = append(kinds, o.Kind)
			}
			if !slices.Equal(kinds, []acp.PermissionOptionKind{acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindRejectOnce}) {
				t.Fatalf("options %v", kinds)
			}
			want := "Answer: " + fmt.Sprint(allow)
			if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); got != want {
				t.Fatalf("reply %q, want %q", got, want)
			}
		})
	}
}

func TestCancelAnswersAWaitingDialog(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	release := make(chan struct{})
	defer close(release)
	asked := make(chan struct{}, 1)
	c.answer = func(acp.RequestPermissionRequest) acp.RequestPermissionResponse {
		asked <- struct{}{}
		<-release // The person hasn't answered.
		return agent.CancelledPermission()
	}
	id := c.newSession(e.folder("a"))
	done := make(chan *acp.PromptResponse, 1)
	go func() {
		response, err := c.prompt(id, "gate-tool")
		if err != nil {
			t.Error(err)
		}
		done <- response
	}()
	select {
	case <-asked:
	case <-time.After(10 * time.Second):
		t.Fatal("not asked")
	}
	ctx, cancel := c.ctx()
	defer cancel()
	if err := c.p.Conn.SessionCancel(ctx, &acp.CancelNotification{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response != nil && response.StopReason != acp.StopReasonCancelled {
			t.Fatalf("stop reason %q", response.StopReason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a waiting dialog blocked the cancellation")
	}
}

func TestTextDialogsAreDeclined(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	c.reset()
	if _, err := c.prompt(id, "ask-for-text"); err != nil {
		t.Fatal(err)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentMessageChunk); got != "Cancelled: true" {
		t.Fatalf("reply %q", got)
	}
	if !strings.Contains(c.text(id, acp.SessionUpdateTypeAgentThoughtChunk), "Your name") {
		t.Fatal("the declined dialog wasn't explained")
	}
}

func TestExtensionCommandWithNoRun(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	c.answer = func(acp.RequestPermissionRequest) acp.RequestPermissionResponse {
		return acp.RequestPermissionResponse{Outcome: acp.SelectedRequestPermissionOutcome("yes")}
	}
	id := c.newSession(e.folder("a"))
	c.reset()
	response, err := c.prompt(id, "extension-command")
	if err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("gave %+v, %v", response, err)
	}
	if got := c.text(id, acp.SessionUpdateTypeAgentThoughtChunk); !strings.Contains(got, "answered true") {
		t.Fatalf("notification %q", got)
	}
}

func TestSecondPromptDuringATurnIsRefused(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	id := c.newSession(e.folder("a"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.prompt(id, "run-slowly")
	}()
	eventually(t, "the turn to stream", func() bool { return c.text(id, acp.SessionUpdateTypeAgentMessageChunk) == "Working" })
	if _, err := c.prompt(id, "another"); code(err) != -32602 {
		t.Fatalf("second prompt gave: %v", err)
	}
	if err := c.load(id, e.folder("a")); code(err) != -32602 {
		t.Fatalf("loading a running session gave: %v", err)
	}
	ctx, cancel := c.ctx()
	defer cancel()
	if err := c.p.Conn.SessionCancel(ctx, &acp.CancelNotification{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	<-done
}

func diffsOf(u acp.SessionUpdate) []acp.ToolCallContent {
	var diffs []acp.ToolCallContent
	for _, block := range toolBlocks(u) {
		if block.Type == acp.ToolCallContentTypeDiff {
			diffs = append(diffs, block)
		}
	}
	return diffs
}

func TestFileToolsReportDiffs(t *testing.T) {
	e := newEnv(t)
	c := e.connect()
	cwd := e.folder("a")
	id := c.newSession(cwd)
	c.reset()
	if _, err := c.prompt(id, "edit-file"); err != nil {
		t.Fatal(err)
	}
	var seen [][]acp.ToolCallContent
	for _, u := range c.received(id) {
		if u.ToolCallID == "call-edit" {
			if diffs := diffsOf(u); diffs != nil {
				seen = append(seen, diffs)
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("diff updates %+v", seen)
	}
	// Before it runs: the replacements it asked for.
	proposed := seen[0]
	if len(proposed) != 2 || proposed[0].Path != filepath.Join(cwd, "a.txt") || *proposed[0].OldText != "two" || proposed[0].NewText != "TWO" {
		t.Fatalf("proposed %+v", proposed)
	}
	// Once done: the applied patch with the lines around it, a diff per hunk.
	applied := seen[1]
	if len(applied) != 2 || *applied[0].OldText != "one\ntwo\nthree\n" || applied[0].NewText != "one\nTWO\nthree\n" || applied[1].NewText != "seven\nEIGHT\n" {
		t.Fatalf("applied %+v", applied)
	}

	c.reset()
	if _, err := c.prompt(id, "write-file"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range c.received(id) {
		if u.ToolCallID == "call-write" && u.Status != nil && *u.Status == acp.ToolCallStatusCompleted {
			diffs := diffsOf(u)
			found = len(diffs) == 1 && diffs[0].OldText == nil && diffs[0].NewText == "fresh\n"
		}
	}
	if !found {
		t.Fatalf("a finished write should keep its diff: %+v", c.received(id))
	}

	// A failed edit shows why, not a change that didn't happen.
	c.reset()
	if _, err := c.prompt(id, "edit-fails"); err != nil {
		t.Fatal(err)
	}
	for _, u := range c.received(id) {
		if u.ToolCallID == "call-bad" && u.Status != nil && *u.Status == acp.ToolCallStatusFailed {
			if diffs := diffsOf(u); diffs != nil {
				t.Fatalf("failed edit kept %+v", diffs)
			}
			if blocks := toolBlocks(u); len(blocks) != 1 || !strings.Contains(blocks[0].Content.Text, "Could not find") {
				t.Fatalf("failed edit shows %+v", blocks)
			}
		}
	}

	// Replay rebuilds the same diffs from Pi's saved result.
	c.p.Close()
	c = e.connect()
	if err := c.load(id, cwd); err != nil {
		t.Fatal(err)
	}
	var replayed []acp.ToolCallContent
	for _, u := range c.received(id) {
		if u.ToolCallID == "call-edit" && u.Status != nil && *u.Status == acp.ToolCallStatusCompleted {
			replayed = diffsOf(u)
		}
	}
	if len(replayed) != 2 || replayed[0].NewText != "one\nTWO\nthree\n" {
		t.Fatalf("replayed %+v", replayed)
	}
}

func TestDetect(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if d := piacp.Detect(ctx, filepath.Join(e.work, "missing"), e.work); d.Found {
		t.Fatalf("missing: %+v", d)
	}
	d := piacp.Detect(ctx, fakePi(t), e.work)
	if !d.Found || d.Err != nil || d.Outdated || !d.SignedIn || d.Version != "1.5.0" || d.Path != fakePi(t) {
		t.Fatalf("ready: %+v", d)
	}
	t.Setenv("FAKEPI_NO_MODELS", "1")
	if d := piacp.Detect(ctx, fakePi(t), e.work); !d.Found || d.SignedIn || d.Err != nil {
		t.Fatalf("signed out: %+v", d)
	}
	t.Setenv("FAKEPI_VERSION", "0.9.2")
	if d := piacp.Detect(ctx, fakePi(t), e.work); !d.Outdated || d.Version != "0.9.2" {
		t.Fatalf("old: %+v", d)
	}
	// Detection leaves no session behind.
	if paths, _ := filepath.Glob(filepath.Join(e.sessions, "*", "*.jsonl")); len(paths) != 0 {
		t.Fatalf("detection wrote %v", paths)
	}
}
