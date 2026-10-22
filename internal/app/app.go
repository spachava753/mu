// Package app owns thread lifecycle, persistence, and background ACP work independently of the UI.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

type Permission struct {
	ID      string
	Request acp.RequestPermissionRequest
}

// Conversation is a transient projection of agent replay and live updates.
// It is deliberately separate from state.Data and is never serialized.
type Conversation struct {
	Entries []state.Entry
	Session state.Session
	Running bool
	Notice  string
}

type thread struct {
	*state.Thread
	*Conversation
}

type ThreadView struct {
	state.Thread
	Conversation
	Status       string
	Error        string
	Permissions  []Permission
	Connected    bool
	Configuring  bool
	SessionError string
}

type Snapshot struct {
	Catalog                            Catalog
	LifecycleError                     string
	Agents                             []state.AgentConfig
	DefaultAgentID                     string
	SidebarCollapsed                   bool
	Threads                            []ThreadView
	LastCommand, LastCwd, StorageError string
}

type pending struct {
	Permission
	answer chan acp.PermissionOptionId
}
type job struct {
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	stop       chan struct{}
	stopping   bool
	promptDone chan struct{}
	stopDone   chan struct{}
}
type runtime struct {
	Conversation
	closing                         chan struct{}
	identity                        [3]string
	process                         *agent.Process
	session                         string
	job                             *job
	status, err                     string
	acceptUpdates                   bool
	pending                         map[string]*pending
	outputs                         map[string]string
	epoch                           uint64
	initializing                    bool
	initialUpdates                  []acp.SessionNotification
	configuring                     bool
	sessionError                    string
	configCancel                    context.CancelFunc
	echoTarget, echoText, echoEntry string
}

type App struct {
	mu                sync.Mutex
	store             *state.Store
	data              state.Data
	runtimes          map[string]*runtime
	changed           chan struct{}
	stopWriter        chan struct{}
	wg                sync.WaitGroup
	closed            bool
	dirty             bool
	storageError      string
	shutdownErrors    []error
	closingSessions   map[[3]string]chan struct{}
	catalog           Catalog
	catalogCancel     context.CancelFunc
	catalogGeneration uint64
}

func Open(dir string) (*App, error) {
	store, data, err := state.Open(dir)
	if err != nil {
		return nil, err
	}
	a := &App{store: store, data: data, runtimes: map[string]*runtime{}, closingSessions: map[[3]string]chan struct{}{}, changed: make(chan struct{}, 1), stopWriter: make(chan struct{})}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-a.stopWriter:
				return
			case <-tick.C:
				a.mu.Lock()
				if a.dirty {
					_ = a.saveLocked()
				}
				a.mu.Unlock()
			}
		}
	}()
	return a, nil
}

func (a *App) Changes() <-chan struct{} { return a.changed }
func (a *App) notifyLocked() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}
func (a *App) saveLocked() error {
	err := a.store.Save(a.data)
	if err != nil {
		a.storageError = "Session references could not be saved: " + err.Error()
		a.dirty = true
	} else {
		a.storageError = ""
		a.dirty = false
	}
	a.notifyLocked()
	return err
}
func (a *App) threadLocked(id string) *thread {
	for i := range a.data.Threads {
		if a.data.Threads[i].ID == id {
			return &thread{&a.data.Threads[i], &a.runtimeLocked(id).Conversation}
		}
	}
	return nil
}
func (a *App) runtimeLocked(id string) *runtime {
	r := a.runtimes[id]
	if r == nil {
		r = &runtime{pending: map[string]*pending{}, outputs: map[string]string{}}
		a.runtimes[id] = r
	}
	return r
}

func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Snapshot{LastCommand: a.data.LastCommand, LastCwd: a.data.LastCwd, StorageError: a.storageError, Agents: slices.Clone(a.data.Agents), DefaultAgentID: a.data.DefaultAgentID, SidebarCollapsed: a.data.SidebarCollapsed}
	for _, t := range a.data.Threads {
		t.AdditionalDirectories = slices.Clone(t.AdditionalDirectories)
		v := ThreadView{Thread: t}
		if r := a.runtimes[t.ID]; r != nil {
			v.Conversation = r.Conversation
			v.Entries = slices.Clone(r.Entries)
			v.Session = r.Session.Clone()
			v.Status, v.Error = r.status, r.err
			v.Connected = r.process != nil && r.process.Alive() && r.session != ""
			v.Configuring, v.SessionError = r.configuring || r.closing != nil, r.sessionError
			for _, p := range r.pending {
				v.Permissions = append(v.Permissions, p.Permission)
			}
			slices.SortFunc(v.Permissions, func(a, b Permission) int { return strings.Compare(a.ID, b.ID) })
		}
		s.Threads = append(s.Threads, v)
	}
	s.Catalog = a.catalogSnapshotLocked()
	if len(a.shutdownErrors) > 0 {
		s.LifecycleError = a.shutdownErrors[len(a.shutdownErrors)-1].Error()
	}
	slices.SortStableFunc(s.Threads, func(a, b ThreadView) int { return b.Updated.Compare(a.Updated) })
	return s
}

func (a *App) Create(command, cwd string) (string, error) {
	return a.create(command, cwd, "", "")
}

func (a *App) create(command, cwd, agentID, agentName string) (string, error) {
	if _, err := agent.ParseCommand(command); err != nil {
		return "", err
	}
	cwd, err := filepath.Abs(strings.TrimSpace(cwd))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("working directory must be a folder")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errors.New("app is closing")
	}
	t := state.Thread{ID: state.ID(), Title: "New thread", Command: command, AgentID: agentID, AgentName: agentName, Cwd: cwd, Created: time.Now(), Updated: time.Now()}
	a.data.Threads = append(a.data.Threads, t)
	oldCmd, oldCwd := a.data.LastCommand, a.data.LastCwd
	a.data.LastCommand, a.data.LastCwd = command, cwd
	if err := a.saveLocked(); err != nil {
		a.data.Threads = a.data.Threads[:len(a.data.Threads)-1]
		a.data.LastCommand, a.data.LastCwd = oldCmd, oldCwd
		return "", err
	}
	return t.ID, nil
}

func (a *App) Archive(id string, archive bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	t := a.threadLocked(id)
	if t == nil {
		return errors.New("thread not found")
	}
	if t.Running || a.configuringLocked(id) {
		return errors.New("wait for session requests and stop the current turn before archiving")
	}
	old := t.Archived
	t.Archived = archive
	if err := a.saveLocked(); err != nil {
		t.Archived = old
		return err
	}
	if archive {
		if r := a.runtimes[id]; r != nil && r.process != nil {
			a.detachLocked(r)
		}
	}
	return nil
}

func (a *App) Delete(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	t := a.threadLocked(id)
	if t == nil {
		return errors.New("thread not found")
	}
	if t.Running || a.configuringLocked(id) {
		return errors.New("wait for session requests and stop the current turn before deleting")
	}
	old := a.data.Threads
	a.data.Threads = slices.DeleteFunc(slices.Clone(old), func(t state.Thread) bool { return t.ID == id })
	if err := a.saveLocked(); err != nil {
		a.data.Threads = old
		return err
	}
	if r := a.runtimes[id]; r != nil && r.process != nil {
		a.detachLocked(r)
	}
	delete(a.runtimes, id)
	return nil
}

func (a *App) Send(id, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("enter a message")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	t := a.threadLocked(id)
	if t == nil {
		return errors.New("thread not found")
	}
	if t.Archived {
		return errors.New("restore this thread before sending")
	}
	if t.Running {
		return errors.New("a turn is already running")
	}
	r := a.runtimeLocked(id)
	if r.configuring || r.closing != nil {
		return errors.New("wait for the session request to finish")
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{ctx: ctx, cancel: cancel, done: make(chan struct{}), stop: make(chan struct{}), promptDone: make(chan struct{})}
	before := *t.Thread
	if t.Title == "New thread" {
		title := []rune(strings.Join(strings.Fields(text), " "))
		if len(title) > 60 {
			title = append(title[:59], '…')
		}
		t.Title = string(title)
	}
	t.Running, t.Updated = true, time.Now()
	if err := a.saveLocked(); err != nil {
		*t.Thread = before
		t.Running = false
		cancel()
		return err
	}
	r.job, r.status, r.err, r.sessionError = j, "Connecting…", "", ""
	r.echoTarget, r.echoText, r.echoEntry = text, "", ""
	a.notifyLocked()
	config := *t.Thread
	a.wg.Add(1)
	go a.run(config, text, r, j)
	return nil
}

func (a *App) run(t state.Thread, text string, r *runtime, j *job) {
	defer a.wg.Done()
	defer j.cancel()
	defer close(j.done)
	p, err := a.connect(j.ctx, t, r)
	a.mu.Lock()
	r.Entries = append(r.Entries, state.Entry{ID: state.ID(), Role: "user", Text: text})
	before := len(r.Entries)
	a.notifyLocked()
	a.mu.Unlock()
	var result acp.PromptResponse
	if err == nil {
		a.mu.Lock()
		r.status, r.acceptUpdates = "Working…", true
		r.echoTarget = text
		sessionID := r.session
		a.notifyLocked()
		a.mu.Unlock()
		result, err = p.Conn.Prompt(j.ctx, acp.PromptRequest{SessionId: acp.SessionId(sessionID), Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
		if err != nil {
			err = p.Error(err)
		}
	}
	close(j.promptDone)
	a.mu.Lock()
	if j.stopDone != nil {
		done := j.stopDone
		a.mu.Unlock()
		<-done
		a.mu.Lock()
	}
	defer a.mu.Unlock()
	r.acceptUpdates = err == nil
	r.echoTarget, r.echoText, r.echoEntry = "", "", ""
	r.status = ""
	if current := a.threadLocked(t.ID); current != nil {
		current.Running, current.Updated = false, time.Now()
		current.Session.TurnUsage = result.Usage
		current.Session.StopReason = result.StopReason
		storeMeta(&current.Session, "prompt", result.Meta)
		if j.stopping || j.ctx.Err() != nil || result.StopReason == acp.StopReasonCancelled {
			current.Entries = append(current.Entries, state.Entry{ID: state.ID(), Role: "system", Text: "Stopped. The response may be incomplete."})
		} else if err != nil {
			r.err = err.Error()
			current.Entries = append(current.Entries, state.Entry{ID: state.ID(), Role: "system", Text: "Agent error: " + r.err})
		} else if len(current.Entries) == before {
			current.Entries = append(current.Entries, state.Entry{ID: state.ID(), Role: "system", Text: "The agent ended the turn without a response. Check its login, model configuration, and provider status before trying again."})
		}
		if err != nil && p != nil {
			a.detachLocked(r)
		}
		_ = a.saveLocked()
	}
	r.job = nil
	a.notifyLocked()
}

// connect always loads saved sessions with replay. A cached transcript is not
// proof of freshness: another client may have changed the agent's history.
func (a *App) connect(ctx context.Context, t state.Thread, r *runtime) (*agent.Process, error) {
	a.mu.Lock()
	p, session, closing := r.process, r.session, r.closing
	identity := [3]string{t.AgentID, t.Command, t.SessionID}
	if barrier := a.closingSessions[identity]; barrier != nil {
		closing = barrier
	}
	a.mu.Unlock()
	if closing != nil {
		select {
		case <-closing:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p != nil && p.Alive() && session != "" {
		return p, nil
	}
	if p != nil {
		p.Close()
	}
	a.mu.Lock()
	r.epoch++
	epoch := r.epoch
	r.initializing, r.initialUpdates, r.acceptUpdates = true, nil, false
	r.session, r.echoTarget, r.echoText, r.echoEntry = "", "", "", ""
	r.outputs = map[string]string{}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); r.initializing = false; r.initialUpdates = nil; a.mu.Unlock() }()
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := agent.Start(initCtx, t.Command, t.Cwd, agent.Hooks{
		Update: func(n acp.SessionNotification) { a.receive(t.ID, r, epoch, n) },
		Terminal: func(session acp.SessionId, terminalID, output string) {
			a.terminalOutput(t.ID, r, epoch, session, terminalID, output)
		},
		Permission: func(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			return a.permission(ctx, t.ID, r, epoch, req)
		},
	})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	r.process, r.identity = p, identity
	a.mu.Unlock()
	var sessionID string
	var options []acp.SessionConfigOption
	var modes *acp.SessionModeState
	var meta map[string]any
	if len(t.AdditionalDirectories) > 0 && p.Info.AgentCapabilities.SessionCapabilities.AdditionalDirectories == nil {
		err = errors.New("agent no longer supports this session's additional workspace directories")
	} else if t.SessionID != "" {
		var loaded acp.LoadSessionResponse
		loaded, err = p.Conn.LoadSession(initCtx, acp.LoadSessionRequest{SessionId: acp.SessionId(t.SessionID), Cwd: t.Cwd, AdditionalDirectories: t.AdditionalDirectories, McpServers: []acp.McpServer{}})
		options, modes, meta = loaded.ConfigOptions, loaded.Modes, loaded.Meta
		sessionID = t.SessionID
		if err != nil {
			err = fmt.Errorf("cannot load session %s: %w. Retry or remove it from Mu; no replacement session was created", t.SessionID, err)
		}
	} else {
		var session acp.NewSessionResponse
		session, err = p.Conn.NewSession(initCtx, acp.NewSessionRequest{Cwd: t.Cwd, McpServers: []acp.McpServer{}})
		sessionID = string(session.SessionId)
		options, modes, meta = session.ConfigOptions, session.Modes, session.Meta
	}
	acknowledged := err == nil && sessionID != ""
	if err == nil && sessionID == "" {
		err = errors.New("agent returned an empty session ID")
	}
	if err == nil {
		err = initCtx.Err()
	}
	a.mu.Lock()
	if err == nil && a.closed {
		err = errors.New("app is closing")
	}
	if err == nil {
		r.session, r.acceptUpdates = sessionID, true
		r.identity[2] = sessionID
		if current := a.threadLocked(t.ID); current != nil {
			// Commit replay only after a successful response; failed loads never
			// replace the previous in-memory projection with partial history.
			current.Entries, current.Session, current.Notice = nil, state.Session{}, ""
			current.SessionID = sessionID
			current.Session.Options, current.Session.Modes = options, modes
			storeMeta(&current.Session, "setup", meta)
			for _, n := range r.initialUpdates {
				if string(n.SessionId) == sessionID {
					a.updateLocked(t.ID, r, n)
				}
			}
			r.initializing, r.initialUpdates = false, nil
			err = a.saveLocked()
		}
	}
	if err != nil {
		// Only close an acknowledged active session. A missing-session error
		// must not be obscured by a second error closing a nonexistent session.
		if acknowledged {
			r.session, r.identity[2] = sessionID, sessionID
		}
		a.detachLocked(r)
	}
	a.mu.Unlock()
	if err != nil {
		return nil, p.Error(err)
	}
	return p, nil
}

func (a *App) Stop(id string) {
	a.mu.Lock()
	r := a.runtimes[id]
	if r == nil || r.job == nil || r.job.stopping {
		a.mu.Unlock()
		return
	}
	j, p, session := r.job, r.process, r.session
	j.stopping = true
	close(j.stop)
	r.status = "Stopping…"
	a.notifyLocked()
	if p == nil || session == "" {
		j.cancel()
		a.mu.Unlock()
		return
	}
	j.stopDone = make(chan struct{})
	a.mu.Unlock()
	go func() {
		defer close(j.stopDone)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Conn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(session)})
		select {
		case <-j.promptDone:
			return
		case <-ctx.Done():
			j.cancel()
			p.Close()
		}
	}()
}

func (a *App) permission(ctx context.Context, id string, r *runtime, epoch uint64, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	a.mu.Lock()
	j := r.job
	if j == nil || j.stopping || a.closed || r.epoch != epoch {
		a.mu.Unlock()
		return agent.CancelledPermission(), nil
	}
	p := &pending{Permission: Permission{ID: state.ID(), Request: req}, answer: make(chan acp.PermissionOptionId, 1)}
	r.pending[p.ID] = p
	a.notifyLocked()
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(r.pending, p.ID); a.notifyLocked(); a.mu.Unlock() }()
	select {
	case <-ctx.Done():
	case <-j.ctx.Done():
	case <-j.stop:
	case option := <-p.answer:
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: option}}}, nil
	}
	return agent.CancelledPermission(), nil
}

func (a *App) Answer(id, permissionID string, option acp.PermissionOptionId) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.runtimes[id]
	if r == nil {
		return
	}
	p := r.pending[permissionID]
	if p == nil {
		return
	}
	for _, o := range p.Request.Options {
		if o.OptionId == option {
			select {
			case p.answer <- option:
			default:
			}
			return
		}
	}
}

// Called with mu held. Invalidate callbacks before teardown and join detached
// close work at shutdown. Reconnecting waits for this close barrier.
func (a *App) detachLocked(r *runtime) {
	p, session := r.process, r.session
	if p == nil {
		return
	}
	r.process, r.session, r.acceptUpdates = nil, "", false
	r.epoch++
	done := make(chan struct{})
	r.closing = done
	identity := r.identity
	if session != "" {
		a.closingSessions[identity] = done
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		err := p.CloseSession(session)
		a.mu.Lock()
		defer a.mu.Unlock()
		if err != nil {
			r.sessionError = err.Error()
			a.shutdownErrors = append(a.shutdownErrors, err)
		}
		if r.closing == done {
			r.closing = nil
		}
		if a.closingSessions[identity] == done {
			delete(a.closingSessions, identity)
		}
		close(done)
		a.notifyLocked()
	}()
}

func (a *App) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	close(a.stopWriter)
	if a.catalogCancel != nil {
		a.catalogCancel()
	}
	for _, r := range a.runtimes {
		if r.job != nil {
			r.job.cancel()
		}
		if r.configCancel != nil {
			r.configCancel()
		}
		a.detachLocked(r)
	}
	a.mu.Unlock()
	a.wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.saveLocked()
	return errors.Join(err, a.store.Close(), errors.Join(a.shutdownErrors...))
}
