package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/state"
)

// Fork asks the agent to fork the thread's session with ACP session/fork, on
// the source's connection. The source stays connected and unchanged. The fork
// response does not include history, so the fork is closed again and its own
// thread loads it, with replay, on the same shared connection when opened.
// It blocks; desktop callers run it off the UI thread. No local transcript is
// sent to the agent or copied to the new view.
//
// A non-empty ID with an error means the fork was created and saved, but
// closing it on the connection failed.
func (a *App) Fork(id string) (string, error) {
	a.mu.Lock()
	source := a.threadLocked(id)
	if a.closed || source == nil {
		a.mu.Unlock()
		return "", errors.New("thread unavailable")
	}
	if source.Archived {
		a.mu.Unlock()
		return "", errors.New("restore the thread before forking")
	}
	if source.Running || a.configuringLocked(id) {
		a.mu.Unlock()
		return "", errors.New("wait for session requests and stop the current turn before forking")
	}
	if source.SessionID == "" {
		a.mu.Unlock()
		return "", errors.New("connect this thread before forking its agent session")
	}
	config, deadline := *source.Thread, a.agentDeadline
	r := a.runtimeLocked(id)
	// connect bounds connecting the source; the fork request has its own deadline.
	ctx, cancel := context.WithCancel(context.Background())
	r.configuring, r.configCancel, r.sessionError = true, cancel, ""
	a.wg.Add(1)
	a.notifyLocked()
	a.mu.Unlock()
	defer a.wg.Done()
	defer cancel()
	var operationErr error
	defer func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		r.configuring, r.configCancel = false, nil
		if operationErr != nil {
			r.sessionError = operationErr.Error()
		}
		a.notifyLocked()
	}()
	fail := func(err error) (string, error) { operationErr = err; return "", err }

	// Use the source's live connection, connecting it first if needed.
	p, err := a.connect(ctx, config, r)
	if err != nil {
		return fail(err)
	}
	a.mu.Lock()
	live := r.live() && r.conn.process == p && r.session == config.SessionID
	a.mu.Unlock()
	if !live {
		return fail(errors.New("the source session is no longer connected"))
	}
	forkCtx, cancelFork := context.WithTimeout(ctx, 35*time.Second)
	defer cancelFork()
	result, err := p.Conn.UnstableForkSession(forkCtx, acp.UnstableForkSessionRequest{SessionId: acp.SessionId(config.SessionID), Cwd: config.Cwd, AdditionalDirectories: config.AdditionalDirectories, McpServers: []acp.UnstableMcpServer{}})
	if err != nil {
		return fail(fmt.Errorf("fork session: %w", p.Error(err)))
	}
	if result.SessionId == "" || string(result.SessionId) == config.SessionID {
		return fail(errors.New("agent returned an invalid fork session ID"))
	}
	// The fork belongs to the agent; record it even if releasing it fails.
	closeErr := p.CloseSession(string(result.SessionId), deadline)

	fork := config
	fork.ID, fork.ParentID, fork.SessionID = state.ID(), id, string(result.SessionId)
	fork.Title, fork.Archived = "Fork: "+config.Title, false
	fork.Created, fork.Updated = time.Now(), time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fail(errors.New("app closed; fork remains with the agent and can be imported"))
	}
	a.data.Threads = append(a.data.Threads, fork)
	if err := a.saveLocked(); err != nil {
		a.data.Threads = a.data.Threads[:len(a.data.Threads)-1]
		return fail(fmt.Errorf("fork created as %s but reference was not saved: %w; use Import sessions", result.SessionId, err))
	}
	if closeErr != nil {
		return fork.ID, fmt.Errorf("fork created, but releasing it from the source connection failed: %w", p.Error(closeErr))
	}
	return fork.ID, nil
}
