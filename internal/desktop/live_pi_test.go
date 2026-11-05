package desktop_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/builtins"
	"github.com/spachava753/mu/internal/desktop"
)

// TestLivePi is a real-provider QA run: the real view and app driving the Pi
// installed on this machine, with its configuration and credentials, against a real
// model, which costs money. It only runs when asked, and AGENTS.md requires
// authorization for the task each time. Pi's sessions go to a temporary directory,
// so its session list stays clean.
//
//	MU_QA_LIVE_PI=$PWD/.qa/live-pi MU_QA_LIVE_MODEL=provider/model \
//	  go test ./internal/desktop -run '^TestLivePi$' -count=1 -v -timeout 40m
func TestLivePi(t *testing.T) {
	out := os.Getenv("MU_QA_LIVE_PI")
	model := os.Getenv("MU_QA_LIVE_MODEL")
	if out == "" || model == "" {
		t.Skip("set MU_QA_LIVE_PI and MU_QA_LIVE_MODEL to run against a real Pi and model")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(out, "transcript.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	t.Setenv("MU_QA_RENDER_DIR", out)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(out, "pi-sessions"))
	dataDir, workDir := filepath.Join(out, "mu-data"), filepath.Join(out, "work")

	l := &live{t: t, log: logFile, out: out, dataDir: dataDir, workDir: workDir}
	l.open()
	defer func() { _ = l.a.Close() }()
	defer l.report()

	// Mu finds the real Pi.
	l.waitPi()
	b := l.a.Snapshot().Builtins[0]
	l.logf("Pi: %+v", b.Status)
	if b.Status.State != app.BuiltinReady {
		t.Fatalf("Pi isn't ready: %+v", b.Status)
	}
	click(t, l.tt, "Settings")
	l.shot("01-settings")
	click(t, l.tt, "Done")
	l.shot("02-new-thread", "built-in adapter")

	// A thread, moved onto the chosen model before anything is sent.
	click(t, l.tt, "Create thread")
	l.waitIdle("the session", 2*time.Minute)
	thread := l.current()
	l.id = thread.ID
	if err := l.a.SetOption(l.id, "model", model); err != nil {
		t.Fatal(err)
	}
	l.waitIdle("the model change", time.Minute)
	if err := l.a.SetOption(l.id, "thinking", "low"); err != nil {
		l.logf("thinking: %v", err)
	}
	l.waitIdle("the thinking change", time.Minute)
	l.requireModel(model)
	l.shot("03-thread-ready", "Send a message to begin.")

	if err := os.WriteFile(filepath.Join(thread.Cwd, "notes.txt"), []byte("first note\nsecond note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thread.Cwd, "data.csv"), []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l.turn("04-list-files", "List the files in the current directory with your tools. Say how many there are and name them.")
	l.check(len(l.tools()) > 0, "listing used a tool")
	l.check(strings.Contains(l.lastReply(), "notes.txt"), "listing named notes.txt")

	l.turn("05-read-file", "Read notes.txt with your read tool and tell me its second line.")
	l.check(strings.Contains(strings.ToLower(l.lastReply()), "second note"), "reading found the second line")

	l.turn("06-write-go", "Create a small Go program in this folder: a go.mod for module primes, and a main.go that prints the first ten prime numbers, one per line. Use your write tool for the files. Then run it with `go run .` and show me the output.")
	mainGo := filepath.Join(thread.Cwd, "main.go")
	before, err := os.ReadFile(mainGo)
	l.check(err == nil && strings.Contains(string(before), "package main"), "main.go was written")
	l.check(strings.Contains(l.lastTurnText(), "29"), "the program's output reached the thread")
	l.expandDiff("06-write-go-diff")

	l.turn("07-edit-go", "Now change main.go so that it prints the first five primes instead of ten. Use your edit tool, not write. Then run it again and show me the output.")
	after, err := os.ReadFile(mainGo)
	l.check(err == nil && string(after) != string(before), "main.go was edited")
	l.check(l.hasDiffWithRemovals(), "the edit shows a diff with removed lines")
	l.expandDiff("07-edit-go-diff")

	l.turn("08-web-search", "Use the parallel MCP server's web search to find the current stable release of Go. Tell me the version and where you found it.")
	l.turn("09-code-mode", "Use your codemode tool to write a short script that calls the parallel web search to look up who created the Go programming language, and print the answer. Tell me what the script printed. Only use the parallel server, no other MCP server.")
	l.expandTools("09-code-mode-tools")
	l.check(l.hasInput(), "code mode shows the script it ran")

	// Enough history for Pi to have something to compact.
	var filler strings.Builder
	for i := range 6000 {
		fmt.Fprintf(&filler, "line %d of some filler text for the context\n", i)
	}
	for _, name := range []string{"big-one.txt", "big-two.txt"} {
		if err := os.WriteFile(filepath.Join(thread.Cwd, name), []byte(filler.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l.turn("10-fill-one", "Read big-one.txt in full with your read tool, using several reads if you need to, and tell me its last line number.")
	l.turn("10-fill-two", "Read big-two.txt in full with your read tool, using several reads if you need to, and tell me its last line number.")
	l.turn("10-compact", "/compact keep the facts about the Go program")
	l.check(strings.Contains(l.lastReply(), "Compacted the context"), "compact ran: "+l.lastReply())

	l.turn("11-after-compact", "What does main.go in this folder print? Answer in one short sentence.")

	// Stop, with a real tool running.
	l.stopTurn("12-stop", "Run `sleep 300` in bash, and then say done.")
	l.turn("13-after-stop", "Reply with just the word ok.")

	// Fork, then the fork works on its own.
	click(t, l.tt, "Fork")
	l.waitFork()
	l.shot("14-fork")
	l.turn("15-on-fork", "Reply with just the word forked.")
	forked := l.id
	l.id = thread.ID

	// Everything again after restarting Mu: history comes from Pi.
	if err := l.a.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	l.open()
	l.waitPi()
	l.tt.Frame()
	click(t, l.tt, "Thread: "+l.titleOf(thread.ID))
	l.waitReplay()
	l.shot("16-replayed")
	l.expandDiff("16-replayed-diff")
	l.requireModel(model)
	l.turn("17-after-restart", "Reply with just the word back.")

	click(t, l.tt, "Import sessions")
	click(t, l.tt, "Find sessions")
	l.waitCatalog()
	l.shot("18-import")
	_ = forked
}

// TestLivePiReplay opens the sessions a TestLivePi run left, with the installed Pi,
// and checks that they read back. It calls no model. MU_QA_LIVE_REPLAY is that run's
// directory.
func TestLivePiReplay(t *testing.T) {
	run := os.Getenv("MU_QA_LIVE_REPLAY")
	if run == "" {
		t.Skip("set MU_QA_LIVE_REPLAY to the directory of a TestLivePi run")
	}
	out := filepath.Join(run, "replay")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(out, "transcript.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	t.Setenv("MU_QA_RENDER_DIR", out)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", filepath.Join(run, "pi-sessions"))
	l := &live{t: t, log: logFile, out: out, dataDir: filepath.Join(out, "mu-data"), workDir: filepath.Join(out, "work")}
	l.open()
	defer func() { _ = l.a.Close() }()
	defer l.report()
	l.waitPi()
	agentID := l.a.Snapshot().Builtins[0].ID()
	if err := l.a.Discover(agentID); err != nil {
		t.Fatal(err)
	}
	l.waitCatalog()
	catalog := l.a.Snapshot().Catalog
	var sessions []string
	for _, s := range catalog.Sessions {
		sessions = append(sessions, s.ID)
	}
	ids, err := l.a.Import(catalog.Generation, sessions)
	if err != nil {
		t.Fatal(err)
	}
	l.id = ids[len(ids)-1] // The oldest: the source, not the fork.
	for _, thread := range l.a.Snapshot().Threads {
		if thread.ID == l.id {
			l.tt.Frame()
			click(t, l.tt, "Thread: "+thread.Title)
		}
	}
	if err := l.a.Connect(l.id); err != nil {
		t.Fatal(err)
	}
	l.waitReplay()
	l.shot("replayed")
	l.expandDiff("replayed-diff")
}

// live drives one run, and records what it sees.
type live struct {
	t                *testing.T
	log              *os.File
	out              string
	dataDir, workDir string
	a                *app.App
	view             *desktop.View
	tt               *ui.Tester
	id               string
	turnStart        int
	slowest          time.Duration
	frames           int
	problems         []string
}

func (l *live) open() {
	l.t.Helper()
	a, err := app.Open(l.dataDir, l.workDir, builtins.Pi(filepath.Join(l.dataDir, "pi")))
	if err != nil {
		l.t.Fatal(err)
	}
	l.a = a
	l.view = desktop.New(a, "")
	l.tt = ui.NewTester(l.view.Draw, 1100, 780)
	l.tt.SetDark(true)
	l.frame()
}

func (l *live) logf(format string, args ...any) {
	fmt.Fprintf(l.log, format+"\n", args...)
}

// frame draws one frame, as the window's loop would, and keeps the slowest time.
func (l *live) frame() {
	began := time.Now()
	l.tt.Frame()
	l.tt.Advance(16 * time.Millisecond)
	if d := time.Since(began); d > l.slowest {
		l.slowest = d
	}
	l.frames++
}

func (l *live) shot(name string, expected ...string) {
	l.t.Helper()
	l.settle()
	for _, text := range expected {
		if !l.tt.HasText(text) {
			l.problem("%s: %q isn't on screen", name, text)
		}
	}
	l.tt.Move(1, 1)
	l.tt.Frame()
	screenshot(l.t, l.tt, name)
}

func (l *live) settle() {
	l.tt.Frame()
	l.tt.Advance(320 * time.Millisecond)
	l.tt.Frame()
}

func (l *live) problem(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	l.problems = append(l.problems, message)
	l.logf("PROBLEM: %s", message)
	l.t.Error(message)
}

func (l *live) check(ok bool, what string) {
	if ok {
		l.logf("ok: %s", what)
	} else {
		l.problem("failed: %s", what)
	}
}

func (l *live) waitPi() {
	l.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		l.frame()
		if b := l.a.Snapshot().Builtins[0]; !b.Checking && b.Status.State != app.BuiltinUnknown {
			l.frame()
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatal("Pi's check didn't finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// current is the newest thread, or the one this run is using.
func (l *live) current() app.ThreadView {
	s := l.a.Snapshot()
	for _, thread := range s.Threads {
		if l.id == "" || thread.ID == l.id {
			return thread
		}
	}
	return app.ThreadView{}
}

func (l *live) titleOf(id string) string {
	for _, thread := range l.a.Snapshot().Threads {
		if thread.ID == id {
			return thread.Title
		}
	}
	return ""
}

func (l *live) waitIdle(what string, timeout time.Duration) app.ThreadView {
	l.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		l.frame()
		thread := l.current()
		if thread.ID != "" && thread.Connected && !thread.Running && !thread.Configuring {
			l.frame()
			return thread
		}
		if thread.SessionError != "" && !thread.Configuring {
			l.t.Fatalf("%s: %s", what, thread.SessionError)
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("timed out waiting for %s: %+v", what, thread.Thread)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (l *live) requireModel(model string) {
	l.t.Helper()
	for _, option := range l.current().Session.Options {
		if option.ID == "model" {
			current := fmt.Sprint(option.CurrentValue)
			l.logf("model option: %s", current)
			if current != model {
				l.t.Fatalf("on model %q, want %q: not sending anything", current, model)
			}
			return
		}
	}
	l.t.Fatal("no model option: not sending anything")
}

// turn sends through the composer, as a person would, and waits for the turn to end.
func (l *live) turn(name, prompt string) {
	l.t.Helper()
	began := time.Now()
	l.turnStart = len(l.current().Entries)
	l.logf("\n===== %s\n> %s", name, prompt)
	click(l.t, l.tt, "Message")
	l.tt.Type(prompt)
	l.tt.Frame()
	l.tt.Key(0, ui.KeyEnter)
	l.tt.Frame()
	deadline := time.Now().Add(6 * time.Minute)
	for {
		l.frame()
		thread := l.current()
		if !thread.Running && !thread.Configuring && thread.Connected && len(thread.Entries) > l.turnStart {
			break
		}
		if time.Now().After(deadline) {
			l.problem("%s: timed out", name)
			l.a.Stop(l.id)
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	l.frame()
	thread := l.current()
	if thread.Error != "" {
		l.problem("%s: %s", name, thread.Error)
	}
	l.logf("took %s", time.Since(began).Round(time.Millisecond))
	for _, e := range thread.Entries[min(l.turnStart, len(thread.Entries)):] {
		line := fmt.Sprintf("[%s] %s", e.Role, shortened(e.Text, 700))
		if e.Role == "tool" {
			line = fmt.Sprintf("[tool %s | %s | %d files changed] %s", e.Title, e.Status, len(e.Diffs), shortened(e.Text, 700))
		}
		l.logf("%s", line)
	}
	if u := thread.Session.Usage; u != nil {
		l.logf("usage: %d of %d, cost %+v", u.Used, u.Size, u.Cost)
	}
	l.shot(name)
}

func shortened(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func (l *live) tools() []string {
	var titles []string
	thread := l.current()
	for _, e := range thread.Entries[min(l.turnStart, len(thread.Entries)):] {
		if e.Role == "tool" {
			titles = append(titles, e.Title)
		}
	}
	return titles
}

func (l *live) lastReply() string {
	thread := l.current()
	for i := len(thread.Entries) - 1; i >= 0; i-- {
		if thread.Entries[i].Role == "assistant" {
			return thread.Entries[i].Text
		}
	}
	return ""
}

func (l *live) lastTurnText() string {
	var b strings.Builder
	thread := l.current()
	for _, e := range thread.Entries[min(l.turnStart, len(thread.Entries)):] {
		b.WriteString(e.Text + "\n")
	}
	return b.String()
}

func (l *live) hasDiffWithRemovals() bool {
	thread := l.current()
	for _, e := range thread.Entries[min(l.turnStart, len(thread.Entries)):] {
		for _, f := range e.Diffs {
			if f.Removed > 0 && f.Added > 0 {
				return true
			}
		}
	}
	return false
}

// reveal scrolls the conversation until a row is in the window. Rows outside it
// aren't drawn at all.
func (l *live) reveal(label string) {
	l.settle()
	for range 200 {
		if _, ok := l.tt.Find(label); ok {
			break
		}
		l.tt.Scroll(660, 300, 0, -300)
		l.settle()
	}
}

// expandDiff opens the last tool call with a diff, and captures it.
func (l *live) expandDiff(name string) {
	l.t.Helper()
	thread := l.current()
	for i := len(thread.Entries) - 1; i >= 0; i-- {
		if e := thread.Entries[i]; e.Role == "tool" && len(e.Diffs) > 0 {
			l.reveal(e.Title)
			if err := l.tt.Click(e.Title); err != nil {
				l.problem("%s: can't open %q: %v", name, e.Title, err)
				return
			}
			l.shot(name)
			_ = l.tt.Click(e.Title)
			l.settle()
			return
		}
	}
	l.problem("%s: no tool call with a diff", name)
}

// expandTools opens each tool call of the last turn, to see how it reads.
func (l *live) expandTools(name string) {
	l.t.Helper()
	titles := l.tools()
	for _, title := range titles {
		_ = l.tt.Click(title)
		l.settle()
	}
	l.shot(name)
	for _, title := range titles {
		_ = l.tt.Click(title)
		l.settle()
	}
}

func (l *live) hasInput() bool {
	thread := l.current()
	for _, e := range thread.Entries[min(l.turnStart, len(thread.Entries)):] {
		if e.Role == "tool" && strings.Contains(e.Input, "\n") {
			return true
		}
	}
	return false
}

func (l *live) stopTurn(name, prompt string) {
	l.t.Helper()
	l.turnStart = len(l.current().Entries)
	l.logf("\n===== %s\n> %s", name, prompt)
	click(l.t, l.tt, "Message")
	l.tt.Type(prompt)
	l.tt.Frame()
	l.tt.Key(0, ui.KeyEnter)
	deadline := time.Now().Add(3 * time.Minute)
	for {
		l.frame()
		thread := l.current()
		running := false
		for _, e := range thread.Entries[min(l.turnStart, len(thread.Entries)):] {
			running = running || (e.Role == "tool" && e.Status == "in_progress" && strings.Contains(e.Title, "sleep"))
		}
		if running {
			break
		}
		if time.Now().After(deadline) {
			l.problem("%s: the tool never started", name)
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	l.frame()
	l.check(sleeping() > 0, "the tool's process is running before Stop")
	l.shot(name + "-running")
	began := time.Now()
	click(l.t, l.tt, "Stop")
	deadline = time.Now().Add(30 * time.Second)
	for l.current().Running {
		l.frame()
		if time.Now().After(deadline) {
			l.problem("%s: Stop didn't end the turn", name)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.logf("Stop took %s", time.Since(began).Round(time.Millisecond))
	l.check(time.Since(began) < 10*time.Second, "Stop was quick")
	time.Sleep(500 * time.Millisecond)
	l.check(sleeping() == 0, "Stop ended the tool's process")
	l.check(l.current().Connected, "the session is still connected")
	l.shot(name + "-stopped")
}

func sleeping() int {
	output, _ := exec.Command("pgrep", "-fc", "^sleep 300$").Output()
	count := 0
	_, _ = fmt.Sscan(string(output), &count)
	return count
}

func (l *live) waitFork() {
	l.t.Helper()
	before := l.id
	deadline := time.Now().Add(2 * time.Minute)
	for {
		l.frame()
		for _, thread := range l.a.Snapshot().Threads {
			if thread.ParentID == before && thread.Connected && !thread.Configuring {
				l.id = thread.ID
				l.check(len(thread.Entries) > 0, "the fork has the source's history")
				l.frame()
				return
			}
		}
		if time.Now().After(deadline) {
			l.problem("the fork didn't open: %+v", l.a.Snapshot().Threads)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (l *live) waitReplay() {
	l.t.Helper()
	thread := l.waitIdle("the replay", 2*time.Minute)
	files := 0
	tools := 0
	for _, e := range thread.Entries {
		if e.Role == "tool" {
			tools++
			files += len(e.Diffs)
		}
	}
	l.logf("\n===== replay: %d entries, %d tools, %d with diffs", len(thread.Entries), tools, files)
	l.check(tools > 0, "tool calls were replayed")
	l.check(files > 0, "diffs were replayed")
}

func (l *live) waitCatalog() {
	l.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		l.frame()
		catalog := l.a.Snapshot().Catalog
		if catalog.Error != "" {
			l.problem("discovery: %s", catalog.Error)
			return
		}
		if catalog.Complete {
			titles, _ := json.Marshal(catalog.Sessions)
			l.logf("\n===== discovery: %s", shortened(string(titles), 1500))
			l.check(len(catalog.Sessions) >= 2, "discovery lists the session and its fork")
			return
		}
		if time.Now().After(deadline) {
			l.problem("discovery timed out")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (l *live) report() {
	l.logf("\n===== UI: %d frames, slowest %s", l.frames, l.slowest.Round(time.Microsecond))
	l.logf("problems: %d", len(l.problems))
	for _, problem := range l.problems {
		l.logf("  %s", problem)
	}
}
