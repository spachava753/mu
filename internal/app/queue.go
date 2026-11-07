package app

import (
	"errors"
	"slices"
	"strings"

	"github.com/spachava753/mu/internal/state"
)

// QueuedPrompt is a prompt waiting for a turn to end. Like a draft, it exists only in
// memory, and it's up to the user until it goes out. Read-only outside the app.
type QueuedPrompt struct {
	ID       string
	Preview  string // what it says on one line, or the files it has
	Files    int
	Steering bool // the turn was interrupted for it, so it goes when that ends
	parts    []state.Part
}

// previewLength is enough to fill the composer's width, and to know it again by.
const previewLength = 300

func newQueuedPrompt(parts []state.Part, steering bool) QueuedPrompt {
	preview := []rune(strings.Join(strings.Fields(promptTitle(parts)), " "))
	if len(preview) > previewLength {
		preview = append(preview[:previewLength-1], '…')
	}
	files := 0
	for _, part := range parts {
		if part.File != nil {
			files++
		}
	}
	return QueuedPrompt{ID: state.ID(), Preview: string(preview), Files: files, Steering: steering, parts: parts}
}

// add puts a prompt in line: last, or if it's steering, ahead of everything but
// the prompts that were steering first.
func (r *runtime) add(p QueuedPrompt) {
	at := len(r.queue)
	if p.Steering {
		if at = slices.IndexFunc(r.queue, func(q QueuedPrompt) bool { return !q.Steering }); at < 0 {
			at = len(r.queue)
		}
	}
	r.queue = slices.Insert(r.queue, at, p)
}

// holdQueue keeps everything queued from going out by itself.
func (r *runtime) holdQueue() {
	r.queueHeld = true
	r.stopSteering()
}

// stopSteering makes every prompt an ordinary queued one, in the same order.
func (r *runtime) stopSteering() {
	for i := range r.queue {
		r.queue[i].Steering = false
	}
}

// Queue sends a prompt after the running turn and those queued before it, or now if
// nothing is running. While Stop is ending the turn the queue is held, and this is the
// user's next message, so it goes as soon as the turn has stopped (SendsNext).
func (a *App) Queue(id string, parts []state.Part) error {
	return a.submit(id, parts, func(r *runtime) {
		r.add(newQueuedPrompt(parts, r.queueHeld && r.job.stopping))
	})
}

// SendsNext reports whether a prompt submitted now goes as soon as the turn has
// stopped, ahead of the queue, rather than to the back of it.
func (t ThreadView) SendsNext() bool { return t.Running && t.Stopping && t.QueueHeld }

// Steer sends a prompt now: it interrupts the running turn, and goes as soon as that
// has ended, ahead of the queue, which then carries on after it.
func (a *App) Steer(id string, parts []state.Part) error {
	return a.submit(id, parts, func(r *runtime) {
		r.add(newQueuedPrompt(parts, true))
		a.interruptLocked(id, r)
	})
}

// SteerQueued does the same for a prompt already queued. Nothing else in the queue
// changes place.
func (a *App) SteerQueued(id, promptID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errAppClosing
	}
	r, t := a.runtimes[id], a.threadLocked(id)
	if r == nil || t == nil {
		return errors.New("thread not found")
	}
	i := slices.IndexFunc(r.queue, func(q QueuedPrompt) bool { return q.ID == promptID })
	if i < 0 || r.queue[i].Steering {
		return nil // It's gone out, or is about to.
	}
	p := r.queue[i]
	if t.Archived {
		return errors.New("restore this thread before sending")
	}
	if !t.Running {
		if r.configuring || r.closing != nil {
			return errors.New("wait for the session request to finish")
		}
		if err := a.startTurnLocked(t, r, p.parts); err != nil {
			return err
		}
		r.queue = slices.Delete(r.queue, i, i+1)
		a.notifyLocked()
		return nil
	}
	r.queue = slices.Delete(r.queue, i, i+1)
	p.Steering = true
	r.add(p)
	a.interruptLocked(id, r)
	a.notifyLocked()
	return nil
}

// RemoveQueued takes a prompt out of the queue, if it hasn't gone out.
func (a *App) RemoveQueued(id, promptID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r := a.runtimes[id]; r != nil {
		r.queue = slices.DeleteFunc(r.queue, func(q QueuedPrompt) bool { return q.ID == promptID })
		a.notifyLocked()
	}
}

// nextPromptLocked starts the next queued prompt, if it can go now. Called when a
// turn ends, so the thread never appears idle in between, and when a session request
// that had it waiting ends. A prompt that can't start is held, for the user to see.
func (a *App) nextPromptLocked(id string, r *runtime) {
	if a.closed || len(r.queue) == 0 || (r.queueHeld && !r.queue[0].Steering) {
		return
	}
	t := a.threadLocked(id)
	if t == nil || t.Archived || t.Running || r.configuring {
		return
	}
	next := r.queue[0]
	// Unlike a message the user sends, this says nothing about an earlier error, such
	// as a settings change the prompt had waited for.
	sessionError := r.sessionError
	if err := a.startTurnLocked(t, r, next.parts); err != nil {
		r.holdQueue() // Saving failed, which the storage error says.
		return
	}
	r.sessionError = sessionError
	r.queue = slices.Delete(r.queue, 0, 1)
}
