// Package piacp is an ACP agent backed by Pi's RPC mode. It speaks ACP over
// whatever transport it is given, so it does not care whether it runs inside Mu
// or in a process of its own. Pi itself always runs as child processes, one per
// open session, because a Pi process holds a single session and Mu's sessions can
// work at the same time.
package piacp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/acp-sdk/jsonrpc"
)

// MinimumVersion is the oldest Pi this adapter has been tested against.
const MinimumVersion = "1.5.0"

const defaultExecutable = "pi"

const (
	startupTimeout = 30 * time.Second
	commandTimeout = 30 * time.Second
)

// ACP error codes.
const (
	codeInvalidParams = -32602
	codeAuthRequired  = -32000
	codeNotFound      = -32002
)

// Config says how to reach Pi and where the adapter may keep its own small state.
type Config struct {
	// Executable says which Pi to run, and is asked each time one starts, so a
	// change takes effect for the next session. Nil or empty means "pi" from the
	// PATH.
	Executable func() string
	// WorkDir is where a process with no session of its own starts, to ask Pi
	// where it keeps sessions.
	WorkDir string
	// StateDir holds markers for sessions with no file yet. Empty disables them,
	// so that a session that was never sent to can't be reopened.
	StateDir string
	// Diag receives diagnostics, such as an extension's errors. It may be nil.
	Diag io.Writer
}

func (c Config) executable() string {
	if c.Executable != nil {
		if path := c.Executable(); path != "" {
			return path
		}
	}
	return defaultExecutable
}

// Serve runs the agent over transport until it closes or ctx ends. By the time it
// returns, every Pi process it started has been stopped.
func Serve(ctx context.Context, transport acp.Transport, cfg Config) error {
	a := &adapter{cfg: cfg, sessions: map[string]*session{}}
	defer a.closeAll()
	return acp.RunAgent(ctx, transport, func(conn *acp.AgentConnection) any {
		a.conn = conn
		// A cancelled context doesn't by itself end the connection's read.
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		a.stopWatching = stop
		return a
	})
}

type adapter struct {
	cfg          Config
	conn         *acp.AgentConnection
	stopWatching func() bool

	mu       sync.Mutex
	sessions map[string]*session
	root     string
	closed   bool
}

var (
	_ acp.InitializeHandler             = (*adapter)(nil)
	_ acp.SessionNewHandler             = (*adapter)(nil)
	_ acp.SessionLoadHandler            = (*adapter)(nil)
	_ acp.SessionListHandler            = (*adapter)(nil)
	_ acp.SessionCloseHandler           = (*adapter)(nil)
	_ acp.SessionForkHandler            = (*adapter)(nil)
	_ acp.SessionPromptHandler          = (*adapter)(nil)
	_ acp.SessionCancelHandler          = (*adapter)(nil)
	_ acp.SessionSetConfigOptionHandler = (*adapter)(nil)
	_ acp.SessionSetTitleHandler        = (*adapter)(nil)
)

func (a *adapter) diag(format string, args ...any) {
	if a.cfg.Diag != nil {
		fmt.Fprintf(a.cfg.Diag, format+"\n", args...)
	}
}

func invalidParams(format string, args ...any) error {
	return &jsonrpc.Error{Code: codeInvalidParams, Message: fmt.Sprintf(format, args...)}
}

func sessionNotFound(id acp.SessionId) error {
	return &jsonrpc.Error{Code: codeNotFound, Message: fmt.Sprintf("Pi has no session %s", id)}
}

// notFoundError explains a failure to start Pi, in the case that it isn't there.
func notFoundError(err error) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Pi wasn't found. Get it from https://pi.dev, or set the path to its executable: %w", err)
	}
	return err
}

// Initialize checks Pi up front, so that a missing or outdated Pi is reported
// once, plainly, instead of as a failure of whatever is tried first.
func (a *adapter) Initialize(ctx context.Context, _ *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	version, err := piVersion(ctx, a.cfg.executable())
	if err != nil {
		return nil, notFoundError(err)
	}
	if !atLeast(version, MinimumVersion) {
		return nil, fmt.Errorf("Pi %s is older than this adapter supports. It needs %s or newer", version, MinimumVersion)
	}
	return &acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(1),
		AgentInfo:       &acp.Implementation{Name: "pi", Version: version},
		AgentCapabilities: &acp.AgentCapabilities{
			LoadSession:        true,
			PromptCapabilities: &acp.PromptCapabilities{EmbeddedContext: true},
			SessionCapabilities: &acp.SessionCapabilities{
				List:  &acp.SessionListCapabilities{},
				Close: &acp.SessionCloseCapabilities{},
				Fork:  &acp.SessionForkCapabilities{},
				Title: &acp.SessionTitleCapabilities{},
			},
		},
	}, nil
}

var versionNumber = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func piVersion(ctx context.Context, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--version").Output()
	if err != nil {
		return "", err
	}
	version := versionNumber.FindString(string(output))
	if version == "" {
		return "", fmt.Errorf("could not read the version from %q", strings.TrimSpace(string(output)))
	}
	return version, nil
}

// atLeast compares dotted versions that versionNumber accepted.
func atLeast(version, minimum string) bool {
	parts := func(v string) [3]int {
		var out [3]int
		for i, group := range versionNumber.FindStringSubmatch(v)[1:] {
			out[i], _ = strconv.Atoi(group)
		}
		return out
	}
	have, need := parts(version), parts(minimum)
	for i := range have {
		if have[i] != need[i] {
			return have[i] > need[i]
		}
	}
	return true
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// open starts a Pi process for a session and waits until it answers. The session
// isn't registered until the caller has what it needs from it.
func (a *adapter) open(ctx context.Context, cwd string, args ...string) (*session, error) {
	s := newSession(a, cwd)
	p, err := startPi(a.cfg.executable(), cwd, args, s.handleEvent)
	if err != nil {
		return nil, notFoundError(err)
	}
	s.pi = p
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := s.readState(ctx); err != nil {
		p.kill()
		return nil, err
	}
	return s, nil
}

func (a *adapter) register(s *session) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		s.close()
		return errors.New("the agent is shutting down")
	}
	defer a.mu.Unlock()
	a.sessions[string(s.ID())] = s
	return nil
}

func (a *adapter) session(id acp.SessionId) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[string(id)]
}

func (a *adapter) forget(s *session) {
	id := string(s.ID())
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions[id] == s {
		delete(a.sessions, id)
	}
}

// closeAll stops every Pi process, at the same time, since each may take a moment.
func (a *adapter) closeAll() {
	if a.stopWatching != nil {
		a.stopWatching()
	}
	a.mu.Lock()
	a.closed = true
	open := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		open = append(open, s)
	}
	a.sessions = map[string]*session{}
	a.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range open {
		wg.Go(s.close)
	}
	wg.Wait()
}

// SessionNew chooses the session's ID, so that a thread nothing was ever sent in
// can be reopened: Pi writes no file until the first turn.
func (a *adapter) SessionNew(ctx context.Context, req *acp.NewSessionRequest) (*acp.NewSessionResponse, error) {
	if !filepath.IsAbs(req.Cwd) {
		return nil, invalidParams("the working directory must be an absolute path: %q", req.Cwd)
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	s, err := a.newEmpty(ctx, id, req.Cwd)
	if err != nil {
		return nil, err
	}
	options, err := s.announce(ctx)
	if err != nil {
		s.close()
		a.forgetUnsent(id)
		return nil, err
	}
	if err := a.register(s); err != nil {
		return nil, err
	}
	return &acp.NewSessionResponse{SessionID: acp.SessionId(id), ConfigOptions: &options}, nil
}

func (a *adapter) newEmpty(ctx context.Context, id, cwd string) (*session, error) {
	s, err := a.open(ctx, cwd, "--session-id", id)
	if err != nil {
		return nil, err
	}
	if err := a.markUnsent(id, cwd); err != nil {
		a.diag("could not record new session %s: %v", id, err)
	}
	return s, nil
}

// SessionLoad reopens a session and replays it. `--session-id` is what finds it:
// it opens the file for that ID in the working folder, and otherwise starts an
// empty session with that ID, which is only right if this session never had a
// file. Reopening a session that's already open reuses its process.
func (a *adapter) SessionLoad(ctx context.Context, req *acp.LoadSessionRequest) (*acp.LoadSessionResponse, error) {
	id := string(req.SessionID)
	if !validID.MatchString(id) || !filepath.IsAbs(req.Cwd) {
		return nil, sessionNotFound(req.SessionID)
	}
	s := a.session(req.SessionID)
	if s != nil {
		if s.cwd != req.Cwd {
			return nil, invalidParams("session %s works in %s, not %s", id, s.cwd, req.Cwd)
		}
		if s.busy() {
			return nil, invalidParams("session %s is running a turn", id)
		}
	} else {
		var err error
		if s, err = a.open(ctx, req.Cwd, "--session-id", id); err != nil {
			return nil, err
		}
		switch {
		case s.hasFile():
			a.forgetUnsent(id)
		case !a.isUnsent(id, req.Cwd):
			s.close()
			return nil, sessionNotFound(req.SessionID)
		}
	}
	if err := s.replay(ctx); err != nil {
		s.closeUnlessRegistered()
		return nil, err
	}
	options, err := s.announce(ctx)
	if err != nil {
		s.closeUnlessRegistered()
		return nil, err
	}
	if err := a.register(s); err != nil {
		return nil, err
	}
	return &acp.LoadSessionResponse{ConfigOptions: &options}, nil
}

// SessionClose ends a session's process. A running turn counts as cancelled.
func (a *adapter) SessionClose(_ context.Context, req *acp.CloseSessionRequest) (*acp.CloseSessionResponse, error) {
	s := a.session(req.SessionID)
	if s == nil {
		return nil, sessionNotFound(req.SessionID)
	}
	a.forget(s)
	s.close()
	return &acp.CloseSessionResponse{}, nil
}

// SessionFork makes a new session with `--fork` in a process of its own, which
// leaves the source untouched and its process undisturbed. Pi can't fork a session
// with no file, but a fork of no history is just a new session.
func (a *adapter) SessionFork(ctx context.Context, req *acp.ForkSessionRequest) (*acp.ForkSessionResponse, error) {
	if !filepath.IsAbs(req.Cwd) {
		return nil, invalidParams("the working directory must be an absolute path: %q", req.Cwd)
	}
	path, err := a.sessionPath(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	var s *session
	if path == "" {
		id, err := newSessionID()
		if err != nil {
			return nil, err
		}
		s, err = a.newEmpty(ctx, id, req.Cwd)
		if err != nil {
			return nil, err
		}
	} else if s, err = a.open(ctx, req.Cwd, "--fork", path); err != nil {
		return nil, err
	}
	options, err := s.announce(ctx)
	if err != nil {
		s.close()
		return nil, err
	}
	if err := a.register(s); err != nil {
		return nil, err
	}
	return &acp.ForkSessionResponse{SessionID: s.ID(), ConfigOptions: &options}, nil
}

// sessionPath finds a session's file, or "" if it's a session with none yet.
func (a *adapter) sessionPath(ctx context.Context, id acp.SessionId) (string, error) {
	if s := a.session(id); s != nil {
		if s.hasFile() {
			return s.file(), nil
		}
		return "", nil
	}
	found, err := a.findSession(ctx, string(id))
	if err != nil {
		return "", err
	}
	if found != nil {
		return found.path, nil
	}
	if a.hasMarker(string(id)) {
		return "", nil
	}
	return "", sessionNotFound(id)
}

func (a *adapter) hasMarker(id string) bool {
	if a.cfg.StateDir == "" || !validID.MatchString(id) {
		return false
	}
	_, err := os.Stat(a.unsentPath(id))
	return err == nil
}

func (a *adapter) SessionPrompt(ctx context.Context, req *acp.PromptRequest) (*acp.PromptResponse, error) {
	s := a.session(req.SessionID)
	if s == nil {
		return nil, sessionNotFound(req.SessionID)
	}
	return s.prompt(ctx, req.Prompt)
}

func (a *adapter) SessionCancel(_ context.Context, req *acp.CancelNotification) error {
	if s := a.session(req.SessionID); s != nil {
		s.cancel()
	}
	return nil
}

func (a *adapter) SessionSetConfigOption(ctx context.Context, req *acp.SetSessionConfigOptionRequest) (*acp.SetSessionConfigOptionResponse, error) {
	s := a.session(req.SessionID)
	if s == nil {
		return nil, sessionNotFound(req.SessionID)
	}
	options, err := s.setOption(ctx, string(req.ConfigID), req.Value)
	if err != nil {
		return nil, err
	}
	return &acp.SetSessionConfigOptionResponse{ConfigOptions: options}, nil
}

// SessionSetTitle names a session. A session with a file that isn't open gets a
// short-lived process to do it; one with no file has nowhere to keep the name,
// which Mu keeps anyway.
func (a *adapter) SessionSetTitle(ctx context.Context, req *acp.SetSessionTitleRequest) (*acp.SetSessionTitleResponse, error) {
	title := strings.TrimSpace(req.Title)
	response := &acp.SetSessionTitleResponse{Title: title}
	if s := a.session(req.SessionID); s != nil {
		ctx, cancel := context.WithTimeout(ctx, commandTimeout)
		defer cancel()
		return response, s.pi.call(ctx, "set_session_name", map[string]any{"name": title}, nil)
	}
	found, err := a.findSession(ctx, string(req.SessionID))
	if err != nil {
		return nil, err
	}
	if found == nil {
		if a.hasMarker(string(req.SessionID)) {
			return response, nil
		}
		return nil, sessionNotFound(req.SessionID)
	}
	s, err := a.open(ctx, found.cwd, "--session-id", found.id)
	if err != nil {
		return nil, err
	}
	defer s.close()
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return response, s.pi.call(ctx, "set_session_name", map[string]any{"name": title}, nil)
}
