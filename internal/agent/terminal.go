package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/state"
)

type terminal struct {
	cmd     *exec.Cmd
	output  *tailBuffer
	done    chan struct{}
	session acp.SessionId
	mu      sync.Mutex
	exit    *acp.TerminalExitStatus
}

func (p *Process) CreateTerminal(_ context.Context, req acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.closed:
		return acp.CreateTerminalResponse{}, errors.New("agent disconnected")
	default:
	}
	if len(p.terminals) >= 32 {
		return acp.CreateTerminalResponse{}, errors.New("release an existing terminal before creating more (limit 32)")
	}
	cwd := p.cwd
	if req.Cwd != nil {
		cwd = *req.Cwd
	}
	if !filepath.IsAbs(cwd) {
		return acp.CreateTerminalResponse{}, errors.New("terminal working directory must be absolute")
	}
	limit := 1024 * 1024
	if req.OutputByteLimit != nil {
		limit = max(0, min(*req.OutputByteLimit, limit))
	}
	cmd := exec.Command(req.Command, req.Args...)
	prepareProcess(cmd)
	cmd.Dir, cmd.WaitDelay = cwd, time.Second
	cmd.Env = os.Environ()
	for _, e := range req.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	t := &terminal{cmd: cmd, done: make(chan struct{}), session: req.SessionId, output: &tailBuffer{limit: limit}}
	id := state.ID()
	if p.hooks.Terminal != nil {
		t.output.onWrite = func() {
			out, truncated := t.output.snapshot()
			if truncated {
				out = "[Earlier output truncated]\n" + out
			}
			p.hooks.Terminal(req.SessionId, id, out)
		}
	}
	cmd.Stdout, cmd.Stderr = t.output, t.output
	if err := cmd.Start(); err != nil {
		return acp.CreateTerminalResponse{}, err
	}
	p.terminals[id] = t
	go func() {
		_ = cmd.Wait()
		code := cmd.ProcessState.ExitCode()
		t.mu.Lock()
		killProcess(cmd)
		t.exit = &acp.TerminalExitStatus{ExitCode: &code}
		t.mu.Unlock()
		close(t.done)
	}()
	return acp.CreateTerminalResponse{TerminalId: id}, nil
}

func (t *terminal) kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exit == nil {
		killProcess(t.cmd)
	}
}

func (p *Process) terminal(id string, session acp.SessionId) (*terminal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.terminals[id]
	if t == nil || t.session != session {
		return nil, fmt.Errorf("unknown terminal %q", id)
	}
	return t, nil
}

func (p *Process) TerminalOutput(_ context.Context, req acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	t, err := p.terminal(req.TerminalId, req.SessionId)
	if err != nil {
		return acp.TerminalOutputResponse{}, err
	}
	out, truncated := t.output.snapshot()
	t.mu.Lock()
	defer t.mu.Unlock()
	return acp.TerminalOutputResponse{Output: out, Truncated: truncated, ExitStatus: t.exit}, nil
}

func (p *Process) WaitForTerminalExit(ctx context.Context, req acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	t, err := p.terminal(req.TerminalId, req.SessionId)
	if err != nil {
		return acp.WaitForTerminalExitResponse{}, err
	}
	select {
	case <-ctx.Done():
		return acp.WaitForTerminalExitResponse{}, ctx.Err()
	case <-p.closed:
		return acp.WaitForTerminalExitResponse{}, errors.New("agent disconnected")
	case <-t.done:
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return acp.WaitForTerminalExitResponse{ExitCode: t.exit.ExitCode, Signal: t.exit.Signal}, nil
}

func (p *Process) KillTerminal(_ context.Context, req acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	t, err := p.terminal(req.TerminalId, req.SessionId)
	if err != nil {
		return acp.KillTerminalResponse{}, err
	}
	t.kill()
	return acp.KillTerminalResponse{}, nil
}

func (p *Process) ReleaseTerminal(ctx context.Context, req acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	t, err := p.terminal(req.TerminalId, req.SessionId)
	if err != nil {
		return acp.ReleaseTerminalResponse{}, err
	}
	t.kill()
	select {
	case <-ctx.Done():
		return acp.ReleaseTerminalResponse{}, ctx.Err()
	case <-t.done:
	}
	p.mu.Lock()
	delete(p.terminals, req.TerminalId)
	p.mu.Unlock()
	return acp.ReleaseTerminalResponse{}, nil
}
