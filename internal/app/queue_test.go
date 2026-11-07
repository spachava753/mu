package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

func parts(text string) []state.Part { return []state.Part{{Text: text}} }

// prompts is what the agent was asked, in order.
func prompts(t *testing.T, command string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(strings.Fields(command)[0]), "fixture-sessions", "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	for line := range strings.Lines(string(data)) {
		var call struct {
			Method string `json:"method"`
			Params struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &call) == nil && call.Method == "session/prompt" {
			said = append(said, call.Params.Prompt[0].Text)
		}
	}
	return said
}

func queueText(t *testing.T, a *App, id, text string) {
	t.Helper()
	if err := a.Queue(id, parts(text)); err != nil {
		t.Fatal(err)
	}
}

func steerText(t *testing.T, a *App, id, text string) {
	t.Helper()
	if err := a.Steer(id, parts(text)); err != nil {
		t.Fatal(err)
	}
}

func queued(v ThreadView) []string {
	var previews []string
	for _, q := range v.Queue {
		previews = append(previews, q.Preview)
	}
	return previews
}

func wantPrompts(t *testing.T, command string, want ...string) {
	t.Helper()
	if got := prompts(t, command); !slices.Equal(got, want) {
		t.Fatalf("the agent was told %q, want %q", got, want)
	}
}

func idleWith(t *testing.T, a *App, id string, previews ...string) ThreadView {
	t.Helper()
	return await(t, a, id, func(v ThreadView) bool { return !v.Running && slices.Equal(queued(v), previews) })
}

func startWaiting(t *testing.T, a *App, args string) (id, command string) {
	t.Helper()
	command = testutil.Agent(t) + " " + args
	id = createTest(t, a, command)
	connected(t, a, id)
	if err := a.Send(id, "wait"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	return id, command
}

// answer allows the thread's pending permission request.
func answer(t *testing.T, a *App, id string) {
	t.Helper()
	v := await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) > 0 })
	a.Answer(id, v.Permissions[0].ID, v.Permissions[0].Request.Options[0].OptionID)
}

func TestQueueSendsInOrderAfterTheTurn(t *testing.T) {
	a := openTest(t, t.TempDir())
	command := testutil.Agent(t) + " session-ui"
	id := createTest(t, a, command)
	connected(t, a, id)
	queueText(t, a, id, "permission") // With nothing running, it goes at once.
	v := await(t, a, id, func(v ThreadView) bool { return v.Running && len(v.Permissions) > 0 })
	if len(v.Queue) != 0 {
		t.Fatalf("queued %q", queued(v))
	}
	queueText(t, a, id, "first")
	queueText(t, a, id, "second")
	wantPrompts(t, command, "permission")
	answer(t, a, id)
	v = idleWith(t, a, id)
	wantPrompts(t, command, "permission", "first", "second")
	if v.QueueHeld || lastText(v) != "Echo: second" {
		t.Fatalf("held %v, last %q", v.QueueHeld, lastText(v))
	}
}

func TestSteerInterruptsAndGoesAheadOfTheQueue(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui")
	queueText(t, a, id, "first")
	queueText(t, a, id, "second")
	steerText(t, a, id, "steer")
	idleWith(t, a, id)
	wantPrompts(t, command, "wait", "steer", "first", "second")
}

func TestSteerWithNothingRunningJustSends(t *testing.T) {
	a := openTest(t, t.TempDir())
	command := testutil.Agent(t) + " session-ui"
	id := createTest(t, a, command)
	connected(t, a, id)
	steerText(t, a, id, "hello")
	idleWith(t, a, id)
	wantPrompts(t, command, "hello")
}

// A queued prompt can steer from anywhere in the queue, leaving the rest in order.
func TestSteerQueuedLeavesTheRestInOrder(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui")
	for _, text := range []string{"first", "second", "third"} {
		queueText(t, a, id, text)
	}
	v := findThread(t, a, id)
	if err := a.SteerQueued(id, v.Queue[1].ID); err != nil {
		t.Fatal(err)
	}
	idleWith(t, a, id)
	wantPrompts(t, command, "wait", "second", "first", "third")
}

// Two steers before the turn has stopped both go first, in the order given. The second
// doesn't interrupt the first, as it hasn't been sent yet to interrupt.
func TestSteersWaitingToGoKeepTheirOrder(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui slow-cancel")
	queueText(t, a, id, "queued")
	steerText(t, a, id, "one")
	steerText(t, a, id, "two")
	if got := queued(findThread(t, a, id)); !slices.Equal(got, []string{"one", "two", "queued"}) {
		t.Fatalf("queued %q", got)
	}
	idleWith(t, a, id)
	wantPrompts(t, command, "wait", "one", "two", "queued")
}

// Once the first of two steers has gone out, the second is queued like any other: it
// can interrupt the first.
func TestSecondSteerCanInterruptTheFirst(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui slow-cancel")
	steerText(t, a, id, "wait")
	steerText(t, a, id, "second")
	v := await(t, a, id, func(v ThreadView) bool {
		return v.Running && len(v.Queue) == 1 && !v.Queue[0].Steering && len(prompts(t, command)) == 2
	})
	if v.QueueHeld {
		t.Fatal("held")
	}
	if err := a.SteerQueued(id, v.Queue[0].ID); err != nil {
		t.Fatal(err)
	}
	idleWith(t, a, id)
	wantPrompts(t, command, "wait", "wait", "second")
}

// And it's held if the first doesn't finish, as anything queued is.
func TestSecondSteerIsHeldWhenTheFirstFails(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui slow-cancel")
	steerText(t, a, id, "prompt-error")
	steerText(t, a, id, "second")
	if v := idleWith(t, a, id, "second"); !v.QueueHeld || v.Queue[0].Steering {
		t.Fatalf("held %v, queue %+v", v.QueueHeld, v.Queue)
	}
	wantPrompts(t, command, "wait", "prompt-error")
}

func TestStopHoldsTheQueueUntilTheNextMessage(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui")
	queueText(t, a, id, "first")
	queueText(t, a, id, "second")
	a.Stop(id)
	v := idleWith(t, a, id, "first", "second")
	if !v.QueueHeld {
		t.Fatal("not held")
	}
	wantPrompts(t, command, "wait")
	// Another message goes now, and the queue carries on after it.
	if err := a.Send(id, "correction"); err != nil {
		t.Fatal(err)
	}
	v = idleWith(t, a, id)
	wantPrompts(t, command, "wait", "correction", "first", "second")
	if v.QueueHeld {
		t.Fatal("still held")
	}
}

// Sending from a held queue is the next message: the rest follow it.
func TestSteerQueuedFromAHeldQueue(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui")
	queueText(t, a, id, "first")
	queueText(t, a, id, "second")
	a.Stop(id)
	v := idleWith(t, a, id, "first", "second")
	if err := a.SteerQueued(id, v.Queue[1].ID); err != nil {
		t.Fatal(err)
	}
	idleWith(t, a, id)
	wantPrompts(t, command, "wait", "second", "first")
}

// Stopping takes back a steer that's waiting for the turn to end: stop means stop.
func TestStopAfterSteerHoldsEverything(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui slow-cancel")
	queueText(t, a, id, "first")
	steerText(t, a, id, "steer")
	a.Stop(id)
	v := idleWith(t, a, id, "steer", "first")
	if !v.QueueHeld || slices.ContainsFunc(v.Queue, func(q QueuedPrompt) bool { return q.Steering }) {
		t.Fatalf("queue %+v, held %v", v.Queue, v.QueueHeld)
	}
	wantPrompts(t, command, "wait")
}

// A message sent while a turn is stopping isn't behind prompts the Stop is holding.
func TestQueueWhileStoppingIsTheNextMessage(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui slow-cancel")
	queueText(t, a, id, "first")
	a.Stop(id)
	queueText(t, a, id, "correction")
	if got := queued(findThread(t, a, id)); !slices.Equal(got, []string{"correction", "first"}) {
		t.Fatalf("queued %q", got)
	}
	idleWith(t, a, id)
	wantPrompts(t, command, "wait", "correction", "first")
}

// A turn that doesn't finish holds the queue, as what's next probably depends on it.
func TestUnfinishedTurnsHoldTheQueue(t *testing.T) {
	for _, tc := range []struct{ prompt, text string }{
		{"prompt-error", "provider overloaded"},
		{"max-tokens", "token limit"},
		{"refuse", "refused"},
		{"empty", "without a response"},
	} {
		t.Run(tc.prompt, func(t *testing.T) {
			a := openTest(t, t.TempDir())
			id, command := startWaiting(t, a, "session-ui")
			queueText(t, a, id, "later")
			steerText(t, a, id, tc.prompt)
			v := idleWith(t, a, id, "later")
			if !v.QueueHeld || !strings.Contains(lastText(v), tc.text) {
				t.Fatalf("held %v, last %q", v.QueueHeld, lastText(v))
			}
			wantPrompts(t, command, "wait", tc.prompt)
		})
	}
}

// An agent that ignores the cancellation still gets the steering prompt, once Stop's
// deadline has closed its session and it has been loaded again.
func TestSteerOverAHungTurn(t *testing.T) {
	a := openTest(t, t.TempDir())
	command := testutil.Agent(t) + " session-ui"
	id := createTest(t, a, command)
	connected(t, a, id)
	if err := a.Send(id, "hang"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return lastText(v) == "Waiting" })
	queueText(t, a, id, "later")
	steerText(t, a, id, "steer")
	v := idleWith(t, a, id)
	wantPrompts(t, command, "hang", "steer", "later")
	if v.Error != "" || lastText(v) != "Echo: later" {
		t.Fatalf("error %q, last %q", v.Error, lastText(v))
	}
}

// A process that dies with a prompt waiting to steer reconnects for it.
func TestSteerAfterTheAgentExits(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui slow-cancel")
	steerText(t, a, id, "exit")
	queueText(t, a, id, "after")
	idleWith(t, a, id, "after")
	wantPrompts(t, command, "wait", "exit")
	v := findThread(t, a, id)
	if !v.QueueHeld || v.Error == "" {
		t.Fatalf("held %v, error %q", v.QueueHeld, v.Error)
	}
	// The queue is there to go on with, on a new process.
	if err := a.SteerQueued(id, v.Queue[0].ID); err != nil {
		t.Fatal(err)
	}
	if v := idleWith(t, a, id); lastText(v) != "Echo: after" {
		t.Fatalf("last %q", lastText(v))
	}
}

// A settings change under way when the turn ends has the next prompt wait for it.
func TestQueueWaitsForSettingsChange(t *testing.T) {
	a := openTest(t, t.TempDir())
	command := testutil.Agent(t) + " session-ui late-config"
	id := createTest(t, a, command)
	connected(t, a, id)
	if err := a.Send(id, "permission"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) > 0 })
	queueText(t, a, id, "later")
	if err := a.SetOption(id, "model", "deep"); err != nil {
		t.Fatal(err)
	}
	answer(t, a, id)
	if v := idleWith(t, a, id); lastText(v) != "Echo: later" {
		t.Fatalf("last %q", lastText(v))
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring })
}

// A rejected settings change stays on screen, and what's queued waits: it may have been
// written for the new setting.
func TestRejectedSettingsHoldTheQueue(t *testing.T) {
	a := openTest(t, t.TempDir())
	command := testutil.Agent(t) + " session-ui late-config"
	id := createTest(t, a, command)
	connected(t, a, id)
	if err := a.Send(id, "permission"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) > 0 })
	queueText(t, a, id, "later")
	if err := a.SetOption(id, "model", "reject"); err != nil {
		t.Fatal(err)
	}
	answer(t, a, id)
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running && !v.Configuring && v.SessionError != "" })
	if !v.QueueHeld || !slices.Equal(queued(v), []string{"later"}) {
		t.Fatalf("held %v, queue %q", v.QueueHeld, queued(v))
	}
	wantPrompts(t, command, "permission")
}

// A steering prompt doesn't wait for the user, but going out doesn't hide an error
// that came up as it waited.
func TestErrorOutlivesAPromptGoingOutByItself(t *testing.T) {
	a := openTest(t, t.TempDir())
	command := testutil.Agent(t) + " session-ui"
	id := createTest(t, a, command)
	connected(t, a, id)
	a.mu.Lock()
	r := a.runtimes[id]
	r.add(newQueuedPrompt(parts("steer"), true))
	r.sessionError, r.queueHeld = "rejected", true
	a.nextPromptLocked(id, r)
	a.mu.Unlock()
	if v := idleWith(t, a, id); v.SessionError != "rejected" {
		t.Fatalf("error %q", v.SessionError)
	}
	wantPrompts(t, command, "steer")
}

// A rejected change during a turn leaves new prompts in line, and not as steering.
func TestRejectedSettingsDuringATurn(t *testing.T) {
	a := openTest(t, t.TempDir())
	command := testutil.Agent(t) + " session-ui"
	id := createTest(t, a, command)
	connected(t, a, id)
	if err := a.Send(id, "permission"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return len(v.Permissions) > 0 })
	if err := a.SetOption(id, "model", "reject"); err != nil {
		t.Fatal(err)
	}
	await(t, a, id, func(v ThreadView) bool { return !v.Configuring && v.SessionError != "" })
	queueText(t, a, id, "later")
	if v := findThread(t, a, id); len(v.Queue) != 1 || v.Queue[0].Steering {
		t.Fatalf("queue %+v", v.Queue)
	}
	answer(t, a, id)
	if v := idleWith(t, a, id, "later"); !v.QueueHeld || v.SessionError == "" {
		t.Fatalf("held %v, error %q", v.QueueHeld, v.SessionError)
	}
	wantPrompts(t, command, "permission")
}

func TestRemoveQueued(t *testing.T) {
	a := openTest(t, t.TempDir())
	id, command := startWaiting(t, a, "session-ui slow-cancel")
	queueText(t, a, id, "first")
	queueText(t, a, id, "second")
	a.RemoveQueued(id, findThread(t, a, id).Queue[0].ID)
	if got := queued(findThread(t, a, id)); !slices.Equal(got, []string{"second"}) {
		t.Fatalf("queued %q", got)
	}
	// Taking back a steer doesn't undo the interruption, so what's left is held.
	steerText(t, a, id, "steer")
	a.RemoveQueued(id, findThread(t, a, id).Queue[0].ID)
	if v := idleWith(t, a, id, "second"); !v.QueueHeld {
		t.Fatal("not held")
	}
	wantPrompts(t, command, "wait")
}

func TestQueueRefusals(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t)+" session-ui")
	connected(t, a, id)
	if err := a.Queue(id, parts("  ")); err == nil {
		t.Fatal("queued nothing")
	}
	if err := a.Steer(id, parts("")); err == nil {
		t.Fatal("steered with nothing")
	}
	if err := a.Archive(id, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Queue(id, parts("hello")); err == nil {
		t.Fatal("queued in an archived thread")
	}
	if err := a.SteerQueued(id, "missing"); err != nil {
		t.Fatalf("a prompt that has gone out: %v", err)
	}
}

// What the queue says for itself is one line, of a bounded length, for any prompt.
func TestQueuedPromptPreview(t *testing.T) {
	p := newQueuedPrompt(parts("one\n\n  two"), false)
	if p.Preview != "one two" || p.Files != 0 {
		t.Fatalf("%+v", p)
	}
	if p := newQueuedPrompt(parts(strings.Repeat("x", 10_000)), false); len([]rune(p.Preview)) != previewLength {
		t.Fatalf("preview of %d", len([]rune(p.Preview)))
	}
}
