// Package agent connects to ACP agents over newline-delimited JSON-RPC on stdio.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
	"github.com/google/shlex"
)

type Hooks struct {
	Update     func(acp.SessionNotification)
	Permission func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

type Process struct {
	Conn   *acp.ClientSideConnection
	Info   acp.InitializeResponse
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer
	sdkLog *tailBuffer // SDK diagnostics, such as undecodable notifications
	done   chan struct{}
	closed chan struct{}
	once   sync.Once
	lifeMu sync.Mutex
	hooks  Hooks
}

func ParseCommand(command string) ([]string, error) {
	args, err := shlex.Split(command)
	if err != nil {
		return nil, fmt.Errorf("agent command: %w", err)
	}
	if len(args) == 0 || args[0] == "" {
		return nil, errors.New("enter an ACP agent executable and its arguments")
	}
	return args, nil
}

func Start(ctx context.Context, command, cwd string, hooks Hooks) (*Process, error) {
	args, err := ParseCommand(command)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	prepareProcess(cmd)
	cmd.WaitDelay = time.Second
	p := &Process{cmd: cmd, hooks: hooks, done: make(chan struct{}), closed: make(chan struct{}), stderr: &tailBuffer{limit: 8192}, sdkLog: &tailBuffer{limit: 4096}}
	cmd.Stderr = p.stderr
	p.stdin, err = cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Own the read end: exec.Wait must not close it before the ACP reader
	// consumes a final response from an agent that exits immediately afterward.
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = p.stdin.Close()
		return nil, err
	}
	p.stdout, cmd.Stdout = reader, writer
	if err = cmd.Start(); err != nil {
		_ = p.stdin.Close()
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("start agent: %w", err)
	}
	_ = writer.Close()
	p.Conn = acp.NewClientSideConnection(p, capabilityWriter{p.stdin}, p.stdout)
	p.Conn.SetLogger(slog.New(slog.NewTextHandler(p.sdkLog, &slog.HandlerOptions{Level: slog.LevelError})))
	go func() {
		_ = cmd.Wait()
		p.lifeMu.Lock()
		killProcess(cmd) // Terminate descendants left in this process group.
		close(p.done)
		p.lifeMu.Unlock()
	}()
	// Mu does not offer client file system or terminal methods: agents use
	// their own. ACP v2 removes both from the protocol.
	info, err := p.Conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientInfo:         &acp.Implementation{Name: "mu", Version: "0.1.0"},
		ClientCapabilities: acp.ClientCapabilities{PlanCapabilities: &acp.PlanCapabilities{}},
	})
	if err == nil && info.ProtocolVersion != acp.ProtocolVersionNumber {
		err = fmt.Errorf("unsupported ACP version %d", info.ProtocolVersion)
	}
	if err == nil {
		err = RequireSessions(info.AgentCapabilities)
	}
	if err != nil {
		p.Close()
		return nil, p.Error(fmt.Errorf("initialize ACP: %w", err))
	}
	p.Info = info
	return p, nil
}

func (p *Process) Alive() bool {
	select {
	case <-p.closed:
		return false
	case <-p.Conn.Done():
		return false
	default:
		return true
	}
}

func (p *Process) Error(err error) error {
	if err == nil {
		return nil
	}
	if output := p.stderr.snapshot(); strings.TrimSpace(output) != "" {
		err = fmt.Errorf("%w\nAgent stderr:\n%s", err, output)
	}
	if output := p.sdkLog.snapshot(); strings.TrimSpace(output) != "" {
		err = fmt.Errorf("%w\nACP diagnostics:\n%s", err, output)
	}
	return err
}

// Grace periods for an agent to exit after its input closes, and then after
// SIGTERM, before it is killed. Agents own session history, so they get the
// chance to persist it.
const (
	exitGrace      = 2 * time.Second
	terminateGrace = time.Second
)

// Close ends the process gracefully: it closes the agent's input, then
// requests termination, and kills it only if it is still running.
func (p *Process) Close() { p.shutdown(true) }

// kill ends an unresponsive process immediately.
func (p *Process) kill() { p.shutdown(false) }

func (p *Process) shutdown(graceful bool) {
	p.once.Do(func() {
		close(p.closed)
		_ = p.stdin.Close()
		signal := func(send func(*exec.Cmd)) {
			p.lifeMu.Lock()
			defer p.lifeMu.Unlock()
			select {
			case <-p.done:
			default:
				send(p.cmd)
			}
		}
		exited := func(grace time.Duration) bool {
			select {
			case <-p.done:
				return true
			case <-time.After(grace):
				return false
			}
		}
		if graceful && !exited(exitGrace) {
			signal(terminateProcess)
			exited(terminateGrace)
		}
		signal(killProcess)
		_ = p.stdout.Close()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	})
}

func (p *Process) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	if p.hooks.Update != nil {
		p.hooks.Update(n)
	}
	return nil
}

func CancelledPermission() acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}
}

func (p *Process) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if p.hooks.Permission == nil {
		return CancelledPermission(), nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-p.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	return p.hooks.Permission(ctx, req)
}

// tailBuffer retains the end of the agent's stderr for error reports.
type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append(b.data[:0], b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.data
	for len(d) > 0 && !utf8.RuneStart(d[0]) {
		d = d[1:]
	}
	return strings.ToValidUTF8(string(d), "�")
}
