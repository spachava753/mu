// Package app owns thread lifecycle, persistence, and background ACP work independently of the UI.
package app

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// Permission is a pending session/request_permission. Request is never
// modified after it arrives.
type Permission struct {
	ID      string
	Request acp.RequestPermissionRequest
	// Tool describes the operation: the tool call Mu has projected from the
	// agent's updates, patched with the fields the request supplies. Requests
	// often name only the tool call ID. It is derived for each snapshot, since
	// the SDK can deliver a request before an earlier tool_call notification.
	Tool PermissionTool
}

// PermissionTool is the visible description of a permission request's tool call.
type PermissionTool struct {
	Title, Kind, Text, Details string
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
	Catalog                   Catalog
	LifecycleError            string
	Agents                    []state.AgentConfig
	Projects                  []state.Project
	DefaultAgentID            string
	SidebarCollapsed          bool
	Threads                   []ThreadView
	LastCommand, StorageError string
	WorkingDirectory          string
}

var errAppClosing = errors.New("app is closing")

type pending struct {
	Permission
	seq       uint64 // order in which Mu received the request
	answer    chan acp.PermissionOptionId
	cancelled chan struct{}
}
type job struct {
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	stopping   bool
	promptDone chan struct{}
	stopDone   chan struct{}
}
type runtime struct {
	Conversation
	closing       chan struct{}
	identity      [3]string
	conn          *connection
	session       string
	job           *job
	status, err   string
	acceptUpdates bool
	pending       map[string]*pending
	tools         map[string]*toolCall
	turnTools     map[string]bool // tool calls reported during the current turn
	modeUpdates   uint64          // current_mode_update notifications received
	// sessionVersion counts changes to Conversation.Session; Snapshot reuses
	// its deep copy until the session changes again.
	sessionVersion, cloneVersion uint64
	sessionClone                 state.Session
	epoch                        uint64
	initializing                 bool
	initialUpdates               []acp.SessionNotification
	configuring                  bool
	sessionError                 string
	configCancel                 context.CancelFunc
	echo                         promptEcho
}

// live reports whether the thread has an open session on a running process.
func (r *runtime) live() bool { return r.conn.alive() && r.session != "" }

// promptEcho tracks the agent's account of the prompt Mu sent this turn.
type promptEcho struct {
	target string // prompt text Mu sent
	local  string // entry ID of Mu's local copy of the prompt
	id     string // agent message ID identified as the echo
	text   string // echoed text received so far
	entry  string // separate entry for a divergent echo without message IDs
	// rejected holds agent message IDs whose first chunk did not start like
	// the prompt; later chunks of those messages are never the echo.
	rejected map[string]bool
}

type App struct {
	mu              sync.Mutex
	store           *state.Store
	data            state.Data
	runtimes        map[string]*runtime
	changed         chan struct{}
	stopWriter      chan struct{}
	wg              sync.WaitGroup
	closed          bool
	dirty           bool
	storageError    string
	shutdownErrors  []error
	closingSessions map[[3]string]chan struct{}
	permissionSeq   uint64
	// ctx ends when the app closes; it bounds requests a Stop must not abandon.
	ctx               context.Context
	cancelCtx         context.CancelFunc
	connections       map[[2]string]*connection
	stopping          map[[2]string]chan struct{} // closed once a configuration's stopped processes exit
	catalog           Catalog
	catalogCancel     context.CancelFunc
	catalogGeneration uint64
	defaultWorkingDir string
	// agentDeadline bounds how long a stopping turn or closing session may
	// take before Mu terminates the agent process. Cancelling can involve
	// finishing a tool call or other cleanup in the agent, so it is generous.
	agentDeadline time.Duration
	// discoveryTimeout bounds one discovery, loadTimeout one session load
	// or creation, and forkTimeout waiting for a fork; tests shorten them.
	discoveryTimeout, loadTimeout, forkTimeout time.Duration
}

// AgentDeadline is how long Mu waits for an agent to end a cancelled turn or
// close a session before terminating its process.
const AgentDeadline = 10 * time.Second

// Open loads Mu's data from dir. New threads get folders in defaultWorkingDir
// until the user chooses another general working directory.
func Open(dir, defaultWorkingDir string) (*App, error) {
	store, data, err := state.Open(dir)
	if err != nil {
		return nil, err
	}
	a := &App{store: store, data: data, defaultWorkingDir: defaultWorkingDir, agentDeadline: AgentDeadline, discoveryTimeout: DiscoveryTimeout, loadTimeout: LoadTimeout, forkTimeout: forkTimeout, runtimes: map[string]*runtime{}, closingSessions: map[[3]string]chan struct{}{}, connections: map[[2]string]*connection{}, stopping: map[[2]string]chan struct{}{}, changed: make(chan struct{}, 1), stopWriter: make(chan struct{})}
	a.ctx, a.cancelCtx = context.WithCancel(context.Background())
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
		r = &runtime{pending: map[string]*pending{}, tools: map[string]*toolCall{}, turnTools: map[string]bool{}, sessionVersion: 1}
		a.runtimes[id] = r
	}
	return r
}

// Snapshot returns the state for the UI. Its values are read-only: nested SDK
// session values are deep copies of the live state, shared between snapshots
// until the session changes.
func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Snapshot{LastCommand: a.data.LastCommand, WorkingDirectory: a.workingDirectoryLocked(), StorageError: a.storageError, Agents: slices.Clone(a.data.Agents), Projects: slices.Clone(a.data.Projects), DefaultAgentID: a.data.DefaultAgentID, SidebarCollapsed: a.data.SidebarCollapsed}
	for _, t := range a.data.Threads {
		t.AdditionalDirectories = slices.Clone(t.AdditionalDirectories)
		v := ThreadView{Thread: t}
		if r := a.runtimes[t.ID]; r != nil {
			v.Conversation = r.Conversation
			v.Entries = slices.Clone(r.Entries)
			if r.cloneVersion != r.sessionVersion {
				r.sessionClone, r.cloneVersion = r.Session.Clone(), r.sessionVersion
			}
			v.Session = r.sessionClone // A deep copy that no one modifies.
			v.Status, v.Error = r.status, r.err
			v.Connected = r.live()
			v.Configuring, v.SessionError = r.configuring || r.closing != nil, r.sessionError
			pending := slices.SortedFunc(maps.Values(r.pending), func(a, b *pending) int { return cmp.Compare(a.seq, b.seq) })
			for _, p := range pending {
				permission := p.Permission
				toolID := string(p.Request.ToolCall.ToolCallId)
				known := r.tools[toolID]
				if r.initializing {
					// While loading, the session's updates are still buffered.
					known = bufferedTool(r.initialUpdates, toolID)
				}
				permission.Tool = permissionTool(known, p.Request.ToolCall)
				v.Permissions = append(v.Permissions, permission)
			}
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

// Create starts a thread with a one-off command. It works in the project's
// folder, or without a project in a new folder of the general working directory.
func (a *App) Create(command, projectID string) (string, error) {
	return a.create(command, "", "", projectID)
}

func (a *App) create(command, agentID, agentName, projectID string) (string, error) {
	if _, err := agent.ParseCommand(command); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errors.New("app is closing")
	}
	cwd, created, err := a.threadDirLocked(projectID)
	if err != nil {
		return "", err
	}
	t := state.Thread{ID: state.ID(), Title: "New thread", Command: command, AgentID: agentID, AgentName: agentName, ProjectID: projectID, Cwd: cwd, Created: time.Now(), Updated: time.Now()}
	a.data.Threads = append(a.data.Threads, t)
	oldCmd := a.data.LastCommand
	a.data.LastCommand = command
	if err := a.saveLocked(); err != nil {
		a.data.Threads = a.data.Threads[:len(a.data.Threads)-1]
		a.data.LastCommand = oldCmd
		if created {
			_ = os.Remove(cwd) // Still empty: no agent has used it.
		}
		return "", err
	}
	return t.ID, nil
}

func (a *App) workingDirectoryLocked() string {
	if a.data.WorkingDirectory != "" {
		return a.data.WorkingDirectory
	}
	return a.defaultWorkingDir
}

// newThreadDir creates a randomly named folder for one thread.
func newThreadDir(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("choose an absolute working directory in Settings")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	for range 8 {
		var b [3]byte
		_, _ = rand.Read(b[:])
		dir := filepath.Join(root, time.Now().Format("2006-01-02")+"-"+hex.EncodeToString(b[:]))
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create thread folder: %w", err)
		}
	}
	return "", errors.New("could not choose a unique thread folder")
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
		if r := a.runtimes[id]; r != nil {
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
	if r := a.runtimes[id]; r != nil {
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
	j := &job{ctx: ctx, cancel: cancel, done: make(chan struct{}), promptDone: make(chan struct{})}
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
	r.echo, r.turnTools = promptEcho{}, map[string]bool{}
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
	local := state.Entry{ID: state.ID(), Role: "user", Text: text}
	r.Entries = append(r.Entries, local)
	before := len(r.Entries)
	a.notifyLocked()
	a.mu.Unlock()
	var result acp.PromptResponse
	if err == nil {
		a.mu.Lock()
		if j.stopping {
			// Stopped while connecting: the prompt is never sent. A Stop just
			// after this check may write session/cancel before the SDK writes
			// the prompt; the agent then ignores it, and Stop's deadline
			// abandons the turn and closes the session.
			a.mu.Unlock()
		} else {
			r.status, r.acceptUpdates = "Working…", true
			r.echo = promptEcho{target: text, local: local.ID}
			sessionID := r.session
			a.notifyLocked()
			a.mu.Unlock()
			result, err = p.Conn.Prompt(j.ctx, acp.PromptRequest{SessionId: acp.SessionId(sessionID), Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
			if err != nil {
				err = p.Error(err)
			}
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
	r.echo = promptEcho{}
	r.status = ""
	if current := a.threadLocked(t.ID); current != nil {
		current.Running, current.Updated = false, time.Now()
		current.Session.TurnUsage = result.Usage
		current.Session.StopReason = result.StopReason
		storeMeta(&current.Session, "prompt", result.Meta)
		r.sessionVersion++
		if j.stopping || j.ctx.Err() != nil || result.StopReason == acp.StopReasonCancelled {
			current.Entries = append(current.Entries, state.Entry{ID: state.ID(), Role: "system", Text: "Stopped. The response may be incomplete."})
		} else if err != nil {
			r.err = err.Error()
			current.Entries = append(current.Entries, state.Entry{ID: state.ID(), Role: "system", Text: "Agent error: " + r.err})
		} else if text := stopReasonText(result.StopReason); text != "" {
			current.Entries = append(current.Entries, state.Entry{ID: state.ID(), Role: "system", Text: text})
		} else if result.StopReason == acp.StopReasonEndTurn && len(current.Entries) == before {
			current.Entries = append(current.Entries, state.Entry{ID: state.ID(), Role: "system", Text: "The agent ended the turn without a response. Check its login, model configuration, and provider status before trying again."})
		}
		// An error response does not end the session. Detach only when the
		// process is gone or Mu abandoned the request, as after Stop's deadline.
		if err != nil && p != nil && (!p.Alive() || j.ctx.Err() != nil) {
			a.detachLocked(r)
		}
		_ = a.saveLocked()
	}
	r.acceptUpdates = r.live()
	r.job = nil
	a.notifyLocked()
}

// stopReasonText explains a turn that ended for a reason other than the agent
// finishing or being cancelled.
func stopReasonText(reason acp.StopReason) string {
	switch reason {
	case acp.StopReasonRefusal:
		// ACP: the prompt and everything after it are not included in the next prompt.
		return "The agent refused this prompt. The prompt and everything after it will not be included in the agent's next prompt."
	case acp.StopReasonMaxTokens:
		return "The agent stopped because it reached its token limit."
	case acp.StopReasonMaxTurnRequests:
		return "The agent stopped because it reached its limit of model requests for this turn."
	}
	return ""
}

// Deadlines for connecting a thread. Starting and initializing the agent is
// bounded separately from creating or loading the session, since a long
// history can take much longer to replay.
const (
	startTimeout = 30 * time.Second
	LoadTimeout  = 2 * time.Minute
	forkTimeout  = 35 * time.Second
)

// connect always loads saved sessions with replay. A cached transcript is not
// proof of freshness: another client may have changed the agent's history.
// The session is opened on the shared connection for the thread's agent.
func (a *App) connect(ctx context.Context, t state.Thread, r *runtime) (*agent.Process, error) {
	a.mu.Lock()
	closing := r.closing
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
	a.mu.Lock()
	if r.live() {
		p := r.conn.process
		a.mu.Unlock()
		return p, nil
	}
	a.detachLocked(r) // A dead process: release it without waiting.
	r.epoch++
	epoch := r.epoch
	r.initializing, r.initialUpdates, r.acceptUpdates = true, nil, false
	r.session, r.echo = "", promptEcho{}
	r.tools = map[string]*toolCall{}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); r.initializing = false; r.initialUpdates = nil; a.mu.Unlock() }()
	initCtx, cancel := context.WithTimeout(ctx, a.loadTimeout)
	defer cancel()
	unsupported := func(p *agent.Process) bool {
		return len(t.AdditionalDirectories) > 0 && p.Info.AgentCapabilities.SessionCapabilities.AdditionalDirectories == nil
	}
	var c *connection
	var p *agent.Process
	var err error
	loading := false
	for {
		// Every attempt, including replacements, is within the load deadline.
		startCtx, cancelStart := context.WithTimeout(initCtx, startTimeout)
		c, err = a.acquire(startCtx, t.AgentID, t.Command)
		cancelStart()
		if err != nil {
			return nil, err
		}
		p = c.process
		if t.SessionID == "" || unsupported(p) {
			break
		}
		// Route before loading so the replay reaches this thread. A close of
		// the session that began meanwhile, such as a late-creation cleanup,
		// must finish first; routing it claims the session against new ones.
		a.mu.Lock()
		err = a.awaitSessionClosedLocked(initCtx, [3]string{t.AgentID, t.Command, t.SessionID})
		if err == nil && !c.alive() {
			// An unanswered close ended the process meanwhile: load on a
			// replacement.
			a.releaseLocked(c)
			a.mu.Unlock()
			if err := initCtx.Err(); err != nil {
				return nil, err
			}
			continue
		}
		if err == nil {
			c.routes[t.SessionID] = route{t.ID, r, epoch}
			loading = true
		}
		a.mu.Unlock()
		break
	}
	var sessionID string
	var options []acp.SessionConfigOption
	var modes *acp.SessionModeState
	var meta map[string]any
	creating := false
	if unsupported(p) {
		err = errors.New("agent no longer supports this session's additional workspace directories")
	} else if t.SessionID == "" {
		creating = true
	}
	if loading {
		var loaded acp.LoadSessionResponse
		loaded, err = p.Conn.LoadSession(initCtx, acp.LoadSessionRequest{SessionId: acp.SessionId(t.SessionID), Cwd: t.Cwd, AdditionalDirectories: t.AdditionalDirectories, McpServers: []acp.McpServer{}})
		options, modes, meta = loaded.ConfigOptions, loaded.Modes, loaded.Meta
		sessionID = t.SessionID
		if err != nil {
			err = fmt.Errorf("cannot load session %s: %w. Retry or remove it from Mu; no replacement session was created", t.SessionID, err)
		}
	}
	if creating {
		a.mu.Lock()
		c.creating++
		a.mu.Unlock()
		var session acp.NewSessionResponse
		// A Stop does not abandon session/new either: the agent may create
		// the session anyway, and only the response says which. The thread
		// keeps it, and run does not send the stopped prompt.
		session, err = awaitCreation(a, c, a.loadTimeout, func(ctx context.Context) (acp.NewSessionResponse, error) {
			return p.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.Cwd, McpServers: []acp.McpServer{}})
		}, func(s acp.NewSessionResponse) acp.SessionId { return s.SessionId })
		sessionID = string(session.SessionId)
		options, modes, meta = session.ConfigOptions, session.Modes, session.Meta
	}
	acknowledged := err == nil && sessionID != ""
	// The SDK delivers notifications sent before a response first. Mark that
	// boundary now: later buffered notifications are newer than the response.
	a.mu.Lock()
	if creating {
		c.creating--
		if acknowledged {
			c.routes[sessionID] = route{t.ID, r, epoch}
			r.initialUpdates = append(c.unrouted[sessionID], r.initialUpdates...)
		}
		delete(c.unrouted, sessionID)
		if c.creating == 0 {
			clear(c.unrouted)
		}
	}
	boundary := len(r.initialUpdates)
	a.mu.Unlock()
	if err == nil && sessionID == "" {
		err = errors.New("agent returned an empty session ID")
	}
	if err == nil {
		if !creating {
			err = initCtx.Err()
		}
	}
	a.mu.Lock()
	if err == nil && a.closed {
		err = errAppClosing
	}
	r.conn, r.identity = c, [3]string{t.AgentID, t.Command, sessionID}
	if err == nil {
		r.session, r.acceptUpdates = sessionID, true
		if current := a.threadLocked(t.ID); current != nil {
			// Commit replay only after a successful response; failed loads never
			// replace the previous in-memory projection with partial history.
			current.Entries, current.Session, current.Notice = nil, state.Session{}, ""
			current.SessionID = sessionID
			replay := func(updates []acp.SessionNotification) {
				for _, n := range updates {
					a.updateLocked(t.ID, r, n)
				}
			}
			// Replay precedes the response, so state the response supplies wins.
			replay(r.initialUpdates[:boundary])
			if options != nil {
				current.Session.Options = options
			}
			if modes != nil {
				current.Session.Modes = modes
			}
			storeMeta(&current.Session, "setup", meta)
			r.sessionVersion++
			replay(r.initialUpdates[boundary:])
			r.initializing, r.initialUpdates = false, nil
			err = a.saveLocked()
		}
	}
	if err != nil {
		// Only close an acknowledged active session. A missing-session error
		// must not be obscured by a second error closing a nonexistent session.
		// A load Mu abandoned may still complete in the agent, so it is closed
		// too; an agent without it answers -32002, which counts as closed.
		if acknowledged {
			r.session = sessionID
		} else if loading && initCtx.Err() != nil {
			r.session = t.SessionID
		} else if t.SessionID != "" {
			delete(c.routes, t.SessionID)
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
	j, session := r.job, r.session
	j.stopping = true
	// ACP requires every pending permission request of a cancelled turn to be
	// answered as cancelled, and asks clients to mark the turn's unfinished
	// tool calls as cancelled when they send session/cancel.
	cancelPermissionsLocked(r)
	if t := a.threadLocked(id); t != nil {
		for toolID := range r.turnTools {
			if call := r.tools[toolID]; call != nil && !call.finished() {
				call.cancelled = true
				renderTool(t, toolID, call)
			}
		}
	}
	r.status = "Stopping…"
	a.notifyLocked()
	if !r.live() {
		j.cancel()
		a.mu.Unlock()
		return
	}
	j.stopDone = make(chan struct{})
	p, deadline := r.conn.process, a.agentDeadline
	a.mu.Unlock()
	go func() {
		defer close(j.stopDone)
		// Only dispatching the cancellation needs a hard deadline: the write
		// can block on an agent that stopped reading, and Bounded then
		// terminates the process.
		if err := p.Bounded(deadline, func(ctx context.Context) error {
			return p.Conn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(session)})
		}); err != nil {
			j.cancel()
			return
		}
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		select {
		case <-j.promptDone:
		case <-timer.C:
			// The agent has not ended the turn. Abandon the prompt; run then
			// closes the session, which ACP requires the agent to treat as a
			// cancellation. Only an unanswered close terminates the process,
			// so other sessions on it are unaffected.
			j.cancel()
		}
	}()
}

// permission shows a request until the user answers it. Agents may ask
// outside a prompt turn too. A request is answered as cancelled when its turn
// is being stopped, when the agent withdraws it or its process closes, or when
// Mu detaches the session (cancelPermissionsLocked).
func (a *App) permission(ctx context.Context, id string, r *runtime, epoch uint64, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	a.mu.Lock()
	if a.closed || r.epoch != epoch || (r.job != nil && r.job.stopping) {
		a.mu.Unlock()
		return agent.CancelledPermission(), nil
	}
	a.permissionSeq++
	p := &pending{Permission: Permission{ID: state.ID(), Request: req}, seq: a.permissionSeq, answer: make(chan acp.PermissionOptionId, 1), cancelled: make(chan struct{})}
	r.pending[p.ID] = p
	a.notifyLocked()
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(r.pending, p.ID); a.notifyLocked(); a.mu.Unlock() }()
	select {
	case <-ctx.Done():
	case <-p.cancelled:
	case option := <-p.answer:
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: option}}}, nil
	}
	return agent.CancelledPermission(), nil
}

// cancelPermissionsLocked answers every pending request of the runtime's
// session as cancelled.
func cancelPermissionsLocked(r *runtime) {
	for id, p := range r.pending {
		close(p.cancelled)
		delete(r.pending, id)
	}
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

// Called with mu held. Unroute the session, then close it on its connection
// and release the connection; the last user of a connection that is not kept
// terminates its process. Reconnecting waits for this close barrier, and
// starting a replacement process waits for the old one to exit (stopLocked).
// Shutdown joins the detached work.
func (a *App) detachLocked(r *runtime) {
	c, session := r.conn, r.session
	if c == nil {
		return
	}
	r.conn, r.session, r.acceptUpdates = nil, "", false
	r.epoch++
	cancelPermissionsLocked(r)
	if rt, ok := c.routes[session]; ok && rt.r == r {
		delete(c.routes, session)
	}
	done := make(chan struct{})
	r.closing = done
	identity, deadline := r.identity, a.agentDeadline
	if session != "" {
		a.closingSessions[identity] = done
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		err := c.process.CloseSession(session, deadline)
		a.mu.Lock()
		a.releaseLocked(c) // A reconnect also waits for any process it stops.
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
		a.mu.Unlock()
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
	a.cancelCtx()
	if a.catalogCancel != nil {
		a.catalogCancel()
	}
	for _, c := range a.connections {
		if !c.started() {
			c.cancel()
		}
	}
	a.stopIdleLocked()
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
