package desktop_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

// These walk the common workflows listed in the mu-visual-qa skill from start to
// finish, through the real controls, at both window sizes. Each step asserts what
// someone doing that work needs next, so these don't need looking at to catch a
// control that has gone or a draft that was lost. With MU_QA_RENDER_DIR set, each step
// also writes a capture named workflow-<name>-<step>-<width>.

// walk is a person using Mu, starting from no threads.
type walk struct {
	t       *testing.T
	name    string
	width   int
	height  int
	a       *app.App
	v       *desktop.View
	tt      *ui.Tester
	agent   string
	command string
}

func eachSize(t *testing.T, walkThrough func(t *testing.T, width, height int)) {
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) { walkThrough(t, size[0], size[1]) })
	}
}

func startWalk(t *testing.T, name string, width, height int, args string) *walk {
	t.Helper()
	agent := testutil.Agent(t)
	w := &walk{t: t, name: name, width: width, height: height, a: testApp(t), agent: agent, command: fmt.Sprintf("%q %s", agent, args)}
	t.Cleanup(w.releaseAgent) // Before the app closes.
	w.v = desktop.New(w.a, w.command)
	w.v.ChooseFiles = func() ([]string, error) { return nil, nil } // As main.go sets it.
	w.tt = ui.NewTester(w.v.Draw, width, height)
	w.tt.SetDark(true)
	w.tt.Frame()
	return w
}

// releaseAgent lets go of agents started with "gate".
func (w *walk) releaseAgent() {
	store := filepath.Join(filepath.Dir(w.agent), "fixture-sessions")
	if err := os.MkdirAll(store, 0o700); err != nil {
		w.t.Error(err)
	}
	if err := os.WriteFile(filepath.Join(store, "release"), nil, 0o600); err != nil {
		w.t.Error(err)
	}
}

// step captures the state the walk has reached.
func (w *walk) step(name string) {
	w.t.Helper()
	screenshot(w.t, w.tt, fmt.Sprintf("workflow-%s-%s-%d", w.name, name, w.width))
}

func (w *walk) click(label string) {
	w.t.Helper()
	click(w.t, w.tt, label)
}

func (w *walk) typed(text string) {
	w.t.Helper()
	w.tt.Type(text)
	w.tt.Frame()
}

func (w *walk) key(mods ui.Modifiers, key ui.Key) {
	w.t.Helper()
	w.tt.Key(mods, key)
	w.tt.Frame()
}

// reachable checks that controls are there and inside the window.
func (w *walk) reachable(labels ...string) {
	w.t.Helper()
	for _, label := range labels {
		r, ok := w.tt.Find(label)
		if !ok {
			w.t.Fatalf("%s isn't there: %q", label, w.tt.Texts())
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > float32(w.width) || r.Y+r.H > float32(w.height) {
			w.t.Fatalf("%s is outside the window: %+v", label, r)
		}
	}
}

func (w *walk) absent(labels ...string) {
	w.t.Helper()
	for _, label := range labels {
		if _, ok := w.tt.Find(label); ok {
			w.t.Fatalf("%s is there", label)
		}
	}
}

// inDraft checks the keyboard is where someone would go on typing.
func (w *walk) inDraft() {
	w.t.Helper()
	if !w.tt.Focused("Message") {
		w.t.Fatal("the draft doesn't have the focus")
	}
}

// settled waits for the first thread that matches to have no request under way.
func (w *walk) settled() {
	w.t.Helper()
	waitView(w.t, w.a, w.tt, func(s app.Snapshot) bool {
		return len(s.Threads) > 0 && !slices.ContainsFunc(s.Threads, func(t app.ThreadView) bool {
			return t.Configuring || t.Running
		})
	})
}

func (w *walk) thread(id string) app.ThreadView {
	w.t.Helper()
	for _, t := range w.a.Snapshot().Threads {
		if t.ID == id {
			return t
		}
	}
	w.t.Fatalf("no thread %s", id)
	return app.ThreadView{}
}

// openFirstThread creates a thread with the form it starts on.
func (w *walk) openFirstThread() string {
	w.t.Helper()
	w.reachable("Create thread")
	w.step("form")
	w.click("Create thread")
	threads := w.a.Snapshot().Threads
	if len(threads) != 1 {
		w.t.Fatalf("%d threads", len(threads))
	}
	return threads[0].ID
}

// noErrors checks nothing is reported in the thread in view.
func (w *walk) noErrors() {
	w.t.Helper()
	for _, t := range w.a.Snapshot().Threads {
		if t.Error != "" || t.SessionError != "" {
			w.t.Fatalf("thread %q reports %q, %q", t.Title, t.Error, t.SessionError)
		}
	}
	w.absent("Dismiss error")
}

// agentCall is a request the agent received.
type agentCall struct {
	Method string `json:"method"`
	Params struct {
		ConfigID string          `json:"configId"`
		Value    json.RawMessage `json:"value"`
		Prompt   []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	} `json:"params"`
}

// said is what the agent received that changes a session or a conversation, in order.
func (w *walk) said() []string {
	w.t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(w.agent), "fixture-sessions", "calls.jsonl"))
	if err != nil {
		w.t.Fatal(err)
	}
	var said []string
	for line := range strings.Lines(string(data)) {
		var call agentCall
		if json.Unmarshal([]byte(line), &call) != nil {
			continue
		}
		switch call.Method {
		case "session/set_config_option":
			said = append(said, call.Params.ConfigID+"="+strings.Trim(string(call.Params.Value), `"`))
		case "session/prompt":
			text := ""
			for _, block := range call.Params.Prompt {
				text += block.Text
			}
			said = append(said, text)
		}
	}
	return said
}

func (w *walk) agentWasTold(want ...string) {
	w.t.Helper()
	if got := w.said(); !slices.Equal(got, want) {
		w.t.Fatalf("the agent was told %q, want %q", got, want)
	}
}

func (w *walk) lastEntry(id string) string {
	w.t.Helper()
	entries := w.thread(id).Entries
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].Text
}

// New thread, first message: create a thread, type, send, and keep going.
func TestWorkflowNewThreadFirstMessage(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "first-message", width, height, "session-ui")
		id := w.openFirstThread()
		w.inDraft()
		w.typed("hello")
		w.reachable("Message", "Send", "Attach files", "Session options")
		w.step("draft")
		w.key(0, ui.KeyEnter)
		w.settled()
		if got := w.lastEntry(id); got != "Echo: hello" {
			t.Fatalf("last entry %q", got)
		}
		if got := w.thread(id).Title; got != "hello" {
			t.Fatalf("title %q", got)
		}
		w.step("replied")
		w.noErrors()
		// The next message follows straight on.
		w.inDraft()
		w.typed("again")
		w.key(0, ui.KeyEnter)
		w.settled()
		if got := w.lastEntry(id); got != "Echo: again" {
			t.Fatalf("last entry %q", got)
		}
		w.agentWasTold("hello", "again")
	})
}

// Sending before the agent has started goes out once it has.
func TestWorkflowFirstMessageBeforeTheAgentStarts(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "first-message-early", width, height, "gate session-ui")
		id := w.openFirstThread()
		if !w.thread(id).Configuring {
			t.Fatal("the agent isn't still starting")
		}
		w.inDraft()
		w.typed("hello")
		w.key(0, ui.KeyEnter)
		if !w.tt.HasText("Sends when ready…") {
			t.Fatalf("no sign it's waiting: %q", w.tt.Texts())
		}
		w.step("waiting")
		w.releaseAgent()
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			return len(s.Threads[0].Entries) >= 2 && !s.Threads[0].Running
		})
		if got := w.lastEntry(id); got != "Echo: hello" {
			t.Fatalf("last entry %q", got)
		}
		w.step("replied")
		w.noErrors()
	})
}

// Choose settings first: pick the model and thinking level, then write and send.
func TestWorkflowChooseSettingsFirst(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "settings-first", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.reachable("Model", "Reasoning")
		message, _ := w.tt.Find("Message")
		w.click("Model")
		w.step("model-menu")
		w.click("Deep")
		w.settled()
		w.tt.Frame()
		if got := w.thread(id).Session.Options[0].CurrentValue; got != "deep" {
			t.Fatalf("model %v", got)
		}
		if !w.tt.HasText("high") {
			t.Fatalf("the dependent thinking level didn't follow: %q", w.tt.Texts())
		}
		w.click("Optional behavior")
		w.click("On")
		w.settled()
		w.tt.Frame()
		if after, _ := w.tt.Find("Message"); after != message {
			t.Fatalf("the draft moved from %+v to %+v", message, after)
		}
		w.step("chosen")
		// Then straight to typing.
		w.inDraft()
		w.typed("plan this")
		w.key(0, ui.KeyEnter)
		w.settled()
		if got := w.lastEntry(id); got != "Echo: plan this" {
			t.Fatalf("last entry %q", got)
		}
		w.agentWasTold("model=deep", "enabled=true", "plan this")
		w.step("replied")
		w.noErrors()
	})
}

// Changing a setting doesn't touch what's being written, or take the keyboard.
func TestWorkflowChooseSettingsWhileWriting(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "settings-while-writing", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("half written")
		w.click("Model")
		w.click("Deep")
		w.settled()
		w.inDraft()
		w.typed(", now finished")
		w.step("draft")
		w.key(0, ui.KeyEnter)
		w.settled()
		if got := w.lastEntry(id); got != "Echo: half written, now finished" {
			t.Fatalf("last entry %q", got)
		}
		w.agentWasTold("model=deep", "half written, now finished")
	})
}

// Abandon a new thread: create one, at once create another, then remove the first.
func TestWorkflowAbandonNewThread(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "abandon", width, height, "session-ui")
		first := w.openFirstThread()
		if err := w.a.Rename(first, "First"); err != nil {
			t.Fatal(err)
		}
		w.click("New thread")
		w.click("Create thread")
		w.settled()
		threads := w.a.Snapshot().Threads
		if len(threads) != 2 {
			t.Fatalf("%d threads", len(threads))
		}
		second := threads[0].ID
		if second == first {
			second = threads[1].ID
		}
		w.step("two-threads")
		if err := w.tt.RightClick("Thread: First"); err != nil {
			t.Fatal(err)
		}
		if err := w.tt.ChooseMenuItem("Remove from Mu…"); err != nil {
			t.Fatal(err)
		}
		w.tt.Frame()
		w.step("confirm")
		w.click("Remove reference")
		w.absent("Thread: First")
		w.noErrors()
		if len(w.a.Snapshot().Threads) != 1 {
			t.Fatalf("threads %+v", w.a.Snapshot().Threads)
		}
		// The thread being worked in is untouched.
		w.reachable("Message", "Send")
		w.inDraft()
		w.typed("carry on")
		w.key(0, ui.KeyEnter)
		w.settled()
		if got := w.lastEntry(second); got != "Echo: carry on" {
			t.Fatalf("last entry %q", got)
		}
		w.step("carried-on")
	})
}

// Abandon a thread whose agent hasn't started yet: navigate away, and it finishes
// behind the scenes, without troubling the thread in view.
func TestWorkflowNavigateAwayFromAStartingThread(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "navigate-away", width, height, "gate session-ui")
		first := w.openFirstThread()
		if err := w.a.Rename(first, "First"); err != nil {
			t.Fatal(err)
		}
		if !w.thread(first).Configuring {
			t.Fatal("the agent isn't still starting")
		}
		// A second thread with a different agent, which can start.
		w.click("New thread")
		w.key(ui.Cmd, ui.KeyA) // The form's field has the focus.
		w.typed(fmt.Sprintf("%q session-ui", w.agent))
		w.click("Create thread")
		var second string
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			for _, thread := range s.Threads {
				if thread.ID != first && thread.Connected && !thread.Configuring {
					second = thread.ID
					return true
				}
			}
			return false
		})
		if !w.thread(first).Configuring {
			t.Fatal("the first thread isn't still starting")
		}
		w.step("second-open")
		// It can't be removed while it starts, and the menu says so by not offering it.
		if err := w.tt.RightClick("Thread: First"); err != nil {
			t.Fatal(err)
		}
		if err := w.tt.ChooseMenuItem("Remove from Mu…"); err == nil {
			t.Fatal("removing a starting thread was offered")
		}
		w.tt.CloseMenu()
		w.tt.Frame()
		w.inDraftAfterClick()
		w.typed("meanwhile")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !w.thread(second).Running && len(w.thread(second).Entries) >= 2 })
		if got := w.lastEntry(second); got != "Echo: meanwhile" {
			t.Fatalf("last entry %q", got)
		}
		// Once it has started it stays as it was, and can go.
		w.releaseAgent()
		w.settled()
		if got := w.thread(first).Title; got != "First" {
			t.Fatalf("title %q", got)
		}
		if got := w.thread(first).Entries; len(got) != 0 {
			t.Fatalf("entries %+v", got)
		}
		w.noErrors()
		w.step("first-started")
		if err := w.tt.RightClick("Thread: First"); err != nil {
			t.Fatal(err)
		}
		if err := w.tt.ChooseMenuItem("Remove from Mu…"); err != nil {
			t.Fatal(err)
		}
		w.tt.Frame()
		w.click("Remove reference")
		w.absent("Thread: First")
		w.reachable("Message", "Send")
		if got := w.lastEntry(second); got != "Echo: meanwhile" {
			t.Fatalf("the conversation in view changed: %q", got)
		}
		w.noErrors()
	})
}

// inDraftAfterClick clicks the draft, for when something else had the focus.
func (w *walk) inDraftAfterClick() {
	w.t.Helper()
	w.click("Message")
	w.inDraft()
}

// Prepare the next message during a turn: keep writing and attaching while the model
// works, and when it's done, press Enter for the next message.
func TestWorkflowPrepareTheNextMessageDuringATurn(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "next-message", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		before, _ := w.tt.Find("Message")
		options, _ := w.tt.Find("Session options")
		w.inDraft()
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return s.Threads[0].Running })

		// Nothing moves or goes away because a turn started.
		w.inDraft()
		w.reachable("Message", "Attach files", "Stop", "Session options", "Model")
		w.absent("Send")
		if got, _ := w.tt.Find("Message"); got != before {
			t.Fatalf("the draft moved from %+v to %+v", before, got)
		}
		if got, _ := w.tt.Find("Session options"); got.H != options.H {
			t.Fatalf("the options went from %+v to %+v", options, got)
		}
		w.step("running")

		// Write, over several lines, and attach things.
		w.typed("next")
		w.key(ui.Shift, ui.KeyEnter)
		w.typed("message")
		paper := filepath.Join(t.TempDir(), "paper.pdf")
		other := filepath.Join(t.TempDir(), "other.pdf")
		for _, path := range []string{paper, other} {
			if err := os.WriteFile(path, []byte("%PDF\x00"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		field, _ := w.tt.Find("Message")
		if !w.tt.DropFiles(field.X+10, field.Y+10, paper, other) {
			t.Fatal("the composer didn't take the files")
		}
		deadline := time.Now().Add(8 * time.Second)
		for {
			w.tt.Frame()
			if _, ok := w.tt.Find("Attached file: other.pdf"); ok && !w.tt.HasText("Reading…") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("files not read: %q", w.tt.Texts())
			}
			time.Sleep(time.Millisecond)
		}
		w.inDraft()
		w.reachable("Message", "Attach files", "Stop", "Remove other.pdf")
		w.step("prepared")
		w.click("Remove other.pdf")
		w.absent("Attached file: other.pdf")
		w.inDraft()

		// Enter queues now, so the hint says so. Writing on doesn't send anything.
		hint := "Enter to queue · Ctrl+Enter to steer"
		if width == 800 {
			hint = "Ctrl+Enter to steer" // All that fits.
		}
		if !w.tt.HasText(hint) {
			t.Fatalf("nothing says what Enter does: %q", w.tt.Texts())
		}
		w.typed(" too")
		w.agentWasTold("wait")

		// The turn ends. The message stays as written, and Enter now sends it.
		w.click("Stop")
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
		w.reachable("Message", "Attach files", "Send", "Attached file: paper.pdf")
		w.absent("Stop")
		w.step("turn-ended")
		w.inDraft()
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			entries := s.Threads[0].Entries
			return !s.Threads[0].Running && len(entries) > 0 && strings.HasPrefix(entries[len(entries)-1].Text, "Echo: next")
		})
		if got := w.lastEntry(id); got != "Echo: next\nmessage too" {
			t.Fatalf("last entry %q", got)
		}
		w.agentWasTold("wait", "next\nmessage too")
		blocks := lastPromptBlocks(t, w.agent)
		if !slices.Equal(blocks, []string{"text", "resource"}) {
			t.Fatalf("blocks %v", blocks)
		}
		w.absent("Remove paper.pdf") // Its card is in the message now, not the draft.
		w.step("sent")
	})
}

// lastPromptBlocks is the types of the last prompt's blocks, in order.
func lastPromptBlocks(t *testing.T, agent string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(agent), "fixture-sessions", "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	for line := range strings.Lines(string(data)) {
		var call struct {
			Method string `json:"method"`
			Params struct {
				Prompt []struct {
					Type string `json:"type"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &call) == nil && call.Method == "session/prompt" {
			blocks = nil
			for _, block := range call.Params.Prompt {
				blocks = append(blocks, block.Type)
			}
		}
	}
	return blocks
}

// A turn that ends by itself leaves the next message ready as well.
func TestWorkflowNextMessageWhenATurnEndsByItself(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "next-message-turn-ends", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("permission")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 1 })
		w.inDraft()
		w.typed("next message")
		w.reachable("Message", "Stop", "Allow once")
		w.step("permission")
		w.click("Allow once")
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
		w.reachable("Message", "Send")
		w.inDraft()
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			return !s.Threads[0].Running && len(s.Threads[0].Entries) > 2
		})
		w.settled()
		if got := w.lastEntry(id); got != "Echo: next message" {
			t.Fatalf("last entry %q", got)
		}
		w.step("sent")
	})
}

// waitFor waits for the thread to be running or not, with these prompts queued.
func (w *walk) waitFor(id string, running bool, queued ...string) {
	w.t.Helper()
	waitView(w.t, w.a, w.tt, func(s app.Snapshot) bool {
		t := w.thread(id)
		if t.Running != running || len(t.Queue) != len(queued) {
			return false
		}
		for i, q := range t.Queue {
			if q.Preview != queued[i] {
				return false
			}
		}
		return true
	})
}

// Queue prompts: Enter during a turn lines them up, and they go in order, each after
// the turn before it.
func TestWorkflowQueuePrompts(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "queue", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("permission")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 1 })
		before, _ := w.tt.Find("Message")
		w.reachable("Queue", "Stop")
		w.step("running")

		// With nothing written, Enter has nothing to queue, and says nothing.
		w.key(0, ui.KeyEnter)
		w.key(ui.Ctrl, ui.KeyEnter)
		w.noErrors()
		w.waitFor(id, true)

		w.typed("first")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true, "first")
		w.inDraft()
		if draft := w.v.DraftOfSelected(); draft != "" {
			t.Fatalf("the draft kept %q", draft)
		}
		w.reachable("Steer: first", "Remove from queue: first", "Message", "Stop", "Allow once")
		if after, _ := w.tt.Find("Message"); after != before {
			t.Fatalf("the draft moved from %+v to %+v", before, after)
		}
		w.step("one-queued")

		// The button does the same as Enter, and files go along.
		w.typed("second")
		paper := filepath.Join(t.TempDir(), "paper.pdf")
		if err := os.WriteFile(paper, []byte("%PDF\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
		field, _ := w.tt.Find("Message")
		if !w.tt.DropFiles(field.X+10, field.Y+10, paper) {
			t.Fatal("the composer didn't take the file")
		}
		deadline := time.Now().Add(8 * time.Second)
		for {
			w.tt.Frame()
			if _, ok := w.tt.Find("Attached file: paper.pdf"); ok && !w.tt.HasText("Reading…") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("file not read: %q", w.tt.Texts())
			}
			time.Sleep(time.Millisecond)
		}
		w.click("Queue")
		w.waitFor(id, true, "first", "second")
		w.inDraft()
		w.absent("Attached file: paper.pdf")
		w.reachable("1 attached")
		w.agentWasTold("permission")
		w.step("two-queued")

		// The turn ends, and they go in turn, with the agent told nothing else.
		w.click("Allow once")
		w.settled()
		w.agentWasTold("permission", "first", "second")
		if blocks := lastPromptBlocks(t, w.agent); !slices.Equal(blocks, []string{"text", "resource"}) {
			t.Fatalf("blocks %v", blocks)
		}
		w.absent("Queued prompts")
		w.reachable("Message", "Send")
		w.inDraft()
		w.step("all-sent")
		w.noErrors()
	})
}

// Steer: Ctrl+Enter interrupts the turn and goes at once. The queue stays as it was,
// and any prompt in it can steer too.
func TestWorkflowSteer(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "steer", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true)
		w.typed("queued first")
		w.key(0, ui.KeyEnter)
		w.typed("queued second")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true, "queued first", "queued second")
		w.step("queued")

		// The steering prompt is the fixture's "wait", so that its turn stays running.
		w.typed("wait")
		w.key(ui.Ctrl, ui.KeyEnter)
		w.inDraft()
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return len(w.said()) == 2 && s.Threads[0].Running })
		w.waitFor(id, true, "queued first", "queued second")
		w.agentWasTold("wait", "wait")
		if !slices.ContainsFunc(w.thread(id).Entries, func(e state.Entry) bool {
			return strings.HasPrefix(e.Text, "Stopped.")
		}) {
			t.Fatalf("no sign the turn was interrupted: %+v", w.thread(id).Entries)
		}
		w.reachable("Steer: queued first", "Steer: queued second", "Stop", "Message")
		w.step("steered")

		// The second in line goes now, and the first stays behind.
		w.click("Steer: queued second")
		w.inDraft()
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running && len(s.Threads[0].Queue) == 0 })
		w.agentWasTold("wait", "wait", "queued second", "queued first")
		if got := w.lastEntry(id); got != "Echo: queued first" {
			t.Fatalf("last entry %q", got)
		}
		w.step("queue-done")
		w.noErrors()
	})
}

// Ctrl+Enter with no turn to interrupt is just sending.
func TestWorkflowSteerWithNothingRunning(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "steer-idle", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("hello")
		w.key(ui.Ctrl, ui.KeyEnter)
		w.settled()
		w.agentWasTold("hello")
		if got := w.lastEntry(id); got != "Echo: hello" {
			t.Fatalf("last entry %q", got)
		}
		w.noErrors()
	})
}

// Read back during a turn: queueing leaves the view where the person scrolled, and
// steering, which sends at once, goes to the latest.
func TestWorkflowQueueWhileReadingBack(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "queue-reading-back", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		for range 4 {
			w.typed("padding\n\npadding\n\npadding\n\npadding\n\npadding\n\npadding")
			w.key(0, ui.KeyEnter)
			w.settled()
		}
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true)
		message, _ := w.tt.Find("Message")
		w.tt.Scroll(message.X+40, 200, 0, -100000)
		w.tt.Frame()
		w.tt.Advance(320 * time.Millisecond)
		w.reachable("Jump to latest")
		w.click("Message")
		w.typed("later")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true, "later")
		w.reachable("Jump to latest", "Steer: later")
		w.step("queued")

		w.click("Steer: later")
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !w.thread(id).Running })
		w.absent("Jump to latest")
		if got := w.lastEntry(id); got != "Echo: later" {
			t.Fatalf("last entry %q", got)
		}
		w.step("steered")
	})
}

// Stop means stop: what's queued stays, but doesn't go by itself. The next message
// goes straight away, and the queue carries on after it.
func TestWorkflowStopHoldsTheQueue(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "stop-holds-queue", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true)
		for _, text := range []string{"later", "unwanted", "last"} {
			w.typed(text)
			w.key(0, ui.KeyEnter)
		}
		w.waitFor(id, true, "later", "unwanted", "last")
		w.click("Stop")
		w.inDraft()
		w.waitFor(id, false, "later", "unwanted", "last")
		w.agentWasTold("wait")
		w.reachable("Send: later", "Remove from queue: later", "Message", "Send")
		if !w.tt.HasText("Paused. These continue after your next message.") {
			t.Fatalf("nothing says why they're waiting: %q", w.tt.Texts())
		}
		w.step("held")

		w.click("Remove from queue: unwanted")
		w.inDraft()
		w.waitFor(id, false, "later", "last")

		// The next message goes straight away, and the rest follow it.
		w.typed("correction")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running && len(s.Threads[0].Queue) == 0 })
		w.agentWasTold("wait", "correction", "later", "last")
		w.step("resumed")

		// Sending one from a held queue goes now.
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true)
		w.typed("held again")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true, "held again")
		w.click("Stop")
		w.waitFor(id, false, "held again")
		w.click("Send: held again")
		w.inDraft()
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running && len(s.Threads[0].Queue) == 0 })
		if got := w.lastEntry(id); got != "Echo: held again" {
			t.Fatalf("last entry %q", got)
		}
		w.noErrors()
	})
}

// Enter straight after Stop is the correction, not the end of the line.
func TestWorkflowCorrectionWhileStopping(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "correction-while-stopping", width, height, "session-ui slow-cancel")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true)
		w.typed("later")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true, "later")
		w.click("Stop")
		// The turn is ending, so what's written now is the next message, and the
		// composer says so.
		if !w.tt.HasText("Enter to send next") {
			t.Fatalf("nothing says Enter sends next: %q", w.tt.Texts())
		}
		w.reachable("Send", "Send: later")
		w.absent("Queue", "Steer: later")
		w.step("stopping-before-typing")
		w.typed("correction")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, true, "correction", "later")
		if !w.tt.HasText("Sends when the turn stops") {
			t.Fatalf("no sign it's going next: %q", w.tt.Texts())
		}
		w.reachable("Remove from queue: correction", "Send: later")
		w.step("stopping")
		w.waitFor(id, false)
		w.agentWasTold("wait", "correction", "later")
	})
}

// Many queued prompts scroll, and leave the draft and its actions in the window, also
// beside a permission request and in the light theme.
func TestWorkflowLongQueue(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "long-queue", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.typed("permissions-two")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 2 })
		before, _ := w.tt.Find("Message")
		for i := range 8 {
			w.typed(fmt.Sprintf("prompt %d with enough text in it to need truncating once it reaches the edge of the row it is shown in, which it does", i))
			w.key(0, ui.KeyEnter)
		}
		w.waitForCount(id, 8)
		w.reachable("Message", "Queue", "Stop", "Allow once", "Queued prompts")
		if got, _ := w.tt.Find("Message"); got != before {
			t.Fatalf("the draft moved from %+v to %+v", before, got)
		}
		list, _ := w.tt.Find("Queued prompts")
		if list.H > 125 {
			t.Fatalf("the queue takes %v", list.H)
		}
		// A long draft too, then the end of the queue.
		for i := range 5 {
			if i > 0 {
				w.key(ui.Shift, ui.KeyEnter)
			}
			w.typed(fmt.Sprintf("line %d", i))
		}
		w.tt.Scroll(list.X+40, list.Y+10, 0, 10000)
		w.tt.Frame()
		w.tt.Advance(320 * time.Millisecond)
		w.reachable("Message", "Queue", "Stop", "Allow once", "Steer: "+w.thread(id).Queue[7].Preview, "Remove from queue: "+w.thread(id).Queue[7].Preview)
		list, _ = w.tt.Find("Queued prompts")
		remove, _ := w.tt.Find("Remove from queue: " + w.thread(id).Queue[7].Preview)
		if remove.X+remove.W > list.X+list.W-8 {
			t.Fatalf("the scroll bar is over the last remove button: %+v in %+v", remove, list)
		}
		w.step("scrolled")
		w.step("dark")
		w.tt.SetDark(false)
		w.tt.Frame()
		w.step("light")
		w.tt.SetDark(true)
		w.click("Stop")
		w.settled()
		w.reachable("Message", "Send", "Queued prompts")
		w.step("held")
	})
}

func (w *walk) waitForCount(id string, n int) {
	w.t.Helper()
	waitView(w.t, w.a, w.tt, func(s app.Snapshot) bool { return len(w.thread(id).Queue) == n })
}

// A turn that ends in an error holds the queue, and the next message gets it going.
func TestWorkflowQueueHeldAfterAnError(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "held-after-error", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("permission")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 1 })
		for _, text := range []string{"prompt-error", "after error", "max-tokens"} {
			w.typed(text)
			w.key(0, ui.KeyEnter)
		}
		w.waitFor(id, true, "prompt-error", "after error", "max-tokens")
		w.click("Allow once")
		w.waitFor(id, false, "after error", "max-tokens")
		if !w.tt.HasText("Paused. These continue after your next message.") || !w.tt.HasText("Agent error: provider overloaded") {
			t.Fatalf("nothing says what happened: %q", w.tt.Texts())
		}
		w.reachable("Send: after error", "Message", "Send")
		w.inDraft()
		w.step("held")

		w.typed("hello")
		w.key(0, ui.KeyEnter)
		w.waitFor(id, false)
		w.agentWasTold("permission", "prompt-error", "hello", "after error", "max-tokens")
		w.typed("again")
		w.key(0, ui.KeyEnter) // Nothing's queued after the limit, so this goes now.
		w.settled()
		w.agentWasTold("permission", "prompt-error", "hello", "after error", "max-tokens", "again")
		w.step("resumed")
	})
}

// Work across threads: each has its own queue, and it goes on out of view.
func TestWorkflowQueueOutOfView(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "queue-out-of-view", width, height, "session-ui")
		first := w.openFirstThread()
		w.settled()
		if err := w.a.Rename(first, "First"); err != nil {
			t.Fatal(err)
		}
		w.inDraft()
		w.typed("permission")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return len(w.thread(first).Permissions) == 1 })
		w.typed("from first")
		w.key(0, ui.KeyEnter)
		w.waitFor(first, true, "from first")
		permission := w.thread(first).Permissions[0]

		w.click("New thread")
		w.click("Create thread")
		w.tt.Frame()
		w.absent("Steer: from first")
		// The first thread's turn ends while the second is in view.
		w.a.Answer(first, permission.ID, permission.Request.Options[0].OptionID)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			return !w.thread(first).Running && w.lastEntry(first) == "Echo: from first"
		})
		if got := w.thread(first).Queue; len(got) != 0 {
			t.Fatalf("queue %+v", got)
		}
		w.absent("Queued prompts")
	})
}

// Stop and correct: the correction can be typed the moment Stop is pressed.
func TestWorkflowStopAndCorrect(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "stop-and-correct", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return s.Threads[0].Running })
		w.step("running")
		w.click("Stop")
		w.inDraft()
		w.typed("no, ")
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
		w.inDraft()
		w.typed("do this")
		w.step("stopped")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			entries := s.Threads[0].Entries
			return !s.Threads[0].Running && len(entries) > 0 && entries[len(entries)-1].Text == "Echo: no, do this"
		})
		w.agentWasTold("wait", "no, do this")
		if got := w.thread(id).Entries[1].Text; got != "Waiting" {
			t.Fatalf("the partial reply went: %q", got)
		}
	})
}

// Work across threads: each keeps its own draft, and switching lands in it.
func TestWorkflowSwitchThreadsWhileOneRuns(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "switch-threads", width, height, "session-ui")
		first := w.openFirstThread()
		w.settled()
		if err := w.a.Rename(first, "First"); err != nil {
			t.Fatal(err)
		}
		w.inDraft()
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return s.Threads[0].Running || s.Threads[1%len(s.Threads)].Running })
		w.typed("draft in first")
		w.click("New thread")
		w.click("Create thread")
		var second string
		for _, thread := range w.a.Snapshot().Threads {
			if thread.ID != first {
				second = thread.ID
			}
		}
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return w.thread(second).Connected && !w.thread(second).Configuring })
		if err := w.a.Rename(second, "Second"); err != nil {
			t.Fatal(err)
		}
		w.tt.Frame()
		w.inDraft()
		w.typed("hello")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !w.thread(second).Running && len(w.thread(second).Entries) >= 2 })
		w.typed("draft in second")
		w.step("second")

		w.click("Thread: First")
		w.inDraft()
		w.reachable("Stop")
		w.typed(", more")
		w.step("back-in-first")
		w.click("Thread: Second")
		w.inDraft()
		w.typed(", more")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			return !w.thread(second).Running && w.lastEntry(second) == "Echo: draft in second, more"
		})
		w.click("Thread: First")
		w.inDraft()
		w.click("Stop")
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !w.thread(first).Running })
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			return !w.thread(first).Running && w.lastEntry(first) == "Echo: draft in first, more"
		})
		if got := w.lastEntry(second); got != "Echo: draft in second, more" {
			t.Fatalf("the other thread changed: %q", got)
		}
	})
}

// Switch models mid-conversation: the history stays, and the next message uses it.
func TestWorkflowSwitchModelMidConversation(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "switch-model", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("hello")
		w.key(0, ui.KeyEnter)
		w.settled()
		w.step("first-reply")
		history := len(w.thread(id).Entries)
		usage := w.thread(id).Session.Usage.Used

		message, _ := w.tt.Find("Message")
		w.click("Model")
		w.click("Deep")
		w.settled()
		w.tt.Frame()
		if got := w.thread(id).Session.Options[0].CurrentValue; got != "deep" {
			t.Fatalf("model %v", got)
		}
		if !w.tt.HasText("Deep") || !w.tt.HasText("high") {
			t.Fatalf("the controls don't show the change: %q", w.tt.Texts())
		}
		if got := w.thread(id).Session.Usage.Used; got == usage {
			t.Fatalf("usage stayed at %d", got)
		}
		if got := len(w.thread(id).Entries); got != history {
			t.Fatalf("history went from %d to %d entries", history, got)
		}
		if got, _ := w.tt.Find("Message"); got != message {
			t.Fatalf("the draft moved from %+v to %+v", message, got)
		}
		w.step("switched")

		w.inDraft()
		w.typed("again")
		w.key(0, ui.KeyEnter)
		w.settled()
		if got := w.lastEntry(id); got != "Echo: again" {
			t.Fatalf("last entry %q", got)
		}
		if got := w.thread(id).Entries[0].Text; got != "hello" {
			t.Fatalf("first entry %q", got)
		}
		w.agentWasTold("hello", "model=deep", "again")
		w.step("second-reply")
		w.noErrors()
	})
}

// Switching models while a turn runs takes effect, and leaves the turn and the
// message being written alone.
func TestWorkflowSwitchModelDuringATurn(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "switch-model-running", width, height, "session-ui")
		id := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("wait")
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return s.Threads[0].Running })
		w.typed("next")
		w.click("Model")
		w.click("Deep")
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			return !s.Threads[0].Configuring && s.Threads[0].Session.Options[0].CurrentValue == "deep"
		})
		if !w.thread(id).Running {
			t.Fatal("switching ended the turn")
		}
		w.reachable("Message", "Stop", "Attach files")
		w.inDraft()
		w.typed(" message")
		w.step("switched")
		w.click("Stop")
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
		w.inDraft()
		w.key(0, ui.KeyEnter)
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool {
			entries := s.Threads[0].Entries
			return !s.Threads[0].Running && len(entries) > 0 && entries[len(entries)-1].Text == "Echo: next message"
		})
		w.agentWasTold("wait", "model=deep", "next message")
	})
}

// A fork that finishes while a dialog is open opens behind it, leaving the keyboard in
// the field being edited. The draft gets it once the dialog closes.
func TestWorkflowForkFinishingBehindADialog(t *testing.T) {
	eachSize(t, func(t *testing.T, width, height int) {
		w := startWalk(t, "fork-behind-dialog", width, height, "session-ui gate-fork")
		source := w.openFirstThread()
		w.settled()
		w.inDraft()
		w.typed("hello")
		w.key(0, ui.KeyEnter)
		w.settled()
		w.click("Fork")
		w.click("New project")
		w.click("Project name")
		w.typed("project name")
		w.releaseAgent()
		waitView(t, w.a, w.tt, func(s app.Snapshot) bool { return len(s.Threads) == 2 })
		w.tt.Frame()
		w.tt.Frame()
		if !w.tt.Focused("Project name") {
			t.Fatal("the fork finishing moved the keyboard out of the field being edited")
		}
		w.typed(" suffix")
		w.step("dialog")
		// Closing it hands the keyboard to the fork's draft.
		w.click("Cancel")
		w.absent("Project name")
		w.inDraft()
		w.typed("in the fork")
		var fork string
		for _, thread := range w.a.Snapshot().Threads {
			if thread.ID != source {
				fork = thread.ID
			}
		}
		w.key(0, ui.KeyEnter)
		w.settled()
		if got := w.lastEntry(fork); got != "Echo: in the fork" {
			t.Fatalf("last entry %q", got)
		}
		w.step("in-fork")
	})
}

// The same over Settings, where the fork opening closes the screen behind the dialog:
// Enter still does what the focused button says.
func TestWorkflowForkFinishingBehindAConfirmation(t *testing.T) {
	a := testApp(t)
	w := &walk{t: t, a: a, width: 1100, height: 780, agent: testutil.Agent(t)}
	t.Cleanup(w.releaseAgent) // Before the app closes.
	config, err := a.SaveAgent(state.AgentConfig{Name: "Fixture", Command: fmt.Sprintf("%q session-ui gate-fork", w.agent)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateWithAgent(config, ""); err != nil {
		t.Fatal(err)
	}
	w.tt = ui.NewTester(desktop.New(a, "").Draw, w.width, w.height)
	w.tt.Frame()
	w.settled()
	w.click("Fork")
	w.click("Settings")
	w.tt.Scroll(660, 400, 0, 10000)
	w.tt.Frame()
	w.click("Remove agent")
	w.key(0, ui.KeyTab)
	if !w.tt.Focused("Remove configuration") {
		t.Fatal("the confirmation didn't get the focus")
	}
	w.releaseAgent()
	waitView(t, a, w.tt, func(s app.Snapshot) bool { return len(s.Threads) == 2 })
	w.tt.Frame()
	w.tt.Frame()
	if !w.tt.Focused("Remove configuration") {
		t.Fatal("the fork finishing moved the keyboard to another button")
	}
	w.key(0, ui.KeyEnter)
	if got := a.Snapshot().Agents; len(got) != 0 {
		t.Fatalf("Enter kept the agent: %+v", got)
	}
}
