package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

// Fork performs agent-native forking. It blocks; desktop callers run it off the
// UI thread. No local transcript is sent to the agent or copied to the new view.
func (a *App) Fork(id string) (string, error) {
	a.mu.Lock()
	source := a.threadLocked(id)
	if a.closed || source == nil {
		a.mu.Unlock()
		return "", errors.New("thread unavailable")
	}
	if source.Running || a.configuringLocked(id) {
		a.mu.Unlock()
		return "", errors.New("wait for session requests and stop the current turn before forking")
	}
	if source.SessionID == "" {
		a.mu.Unlock()
		return "", errors.New("connect this thread before forking its agent session")
	}
	config := *source.Thread
	r := a.runtimeLocked(id)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	r.configuring, r.configCancel, r.sessionError = true, cancel, ""
	// Do not resume a second process against this session until the first closes.
	a.detachLocked(r)
	closing := r.closing
	if barrier := a.closingSessions[[3]string{config.AgentID, config.Command, config.SessionID}]; barrier != nil {
		closing = barrier
	}
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
	if closing != nil {
		select {
		case <-closing:
		case <-ctx.Done():
			return fail(ctx.Err())
		}
	}
	p, err := agent.Start(ctx, config.Command, config.Cwd, agent.Hooks{})
	if err != nil {
		return fail(err)
	}
	defer p.Close()
	if len(config.AdditionalDirectories) > 0 && p.Info.AgentCapabilities.SessionCapabilities.AdditionalDirectories == nil {
		return fail(errors.New("agent does not support this session's additional directories"))
	}
	// Resume is appropriate here: forking needs agent state, not a rendered replay.
	_, err = p.Conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: acp.SessionId(config.SessionID), Cwd: config.Cwd, AdditionalDirectories: config.AdditionalDirectories, McpServers: []acp.McpServer{}})
	if err != nil {
		return fail(fmt.Errorf("resume before fork: %w", p.Error(err)))
	}
	defer func() {
		if err := p.CloseSession(config.SessionID); err != nil {
			a.mu.Lock()
			a.shutdownErrors = append(a.shutdownErrors, err)
			r.sessionError = err.Error()
			a.mu.Unlock()
		}
	}()
	result, err := p.Conn.UnstableForkSession(ctx, acp.UnstableForkSessionRequest{SessionId: acp.SessionId(config.SessionID), Cwd: config.Cwd, AdditionalDirectories: config.AdditionalDirectories})
	if err != nil {
		return fail(fmt.Errorf("fork session: %w", p.Error(err)))
	}
	if result.SessionId == "" || string(result.SessionId) == config.SessionID {
		return fail(errors.New("agent returned an invalid fork session ID"))
	}
	// The fork belongs to the agent even if local persistence subsequently fails.
	// Close it on this temporary connection; opening its bookmark will load it.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, closeErr := p.Conn.CloseSession(closeCtx, acp.CloseSessionRequest{SessionId: result.SessionId})
	closeCancel()
	if closeErr != nil {
		return fail(fmt.Errorf("fork created as %s but closing it failed: %w; discover it through Import sessions", result.SessionId, closeErr))
	}
	config.ID, config.ParentID, config.SessionID = state.ID(), id, string(result.SessionId)
	config.Title, config.Archived = "Fork: "+config.Title, false
	config.Created, config.Updated = time.Now(), time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return fail(errors.New("app closed; fork remains with the agent and can be imported"))
	}
	a.data.Threads = append(a.data.Threads, config)
	if err := a.saveLocked(); err != nil {
		a.data.Threads = a.data.Threads[:len(a.data.Threads)-1]
		return fail(fmt.Errorf("fork created as %s but reference was not saved: %w; use Import sessions", result.SessionId, err))
	}
	return config.ID, nil
}
