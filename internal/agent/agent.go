// Package agent connects to ACP agents over newline-delimited JSON-RPC on stdio.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/shlex"
	"github.com/spachava753/acp-sdk/acp"
)

type Hooks struct {
	Update     func(acp.SessionNotification)
	Permission func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

type Process struct {
	Conn   *acp.Client
	Info   acp.InitializeResponse
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tailBuffer
	diag   *tailBuffer // protocol diagnostics, such as undecodable notifications
	done   chan struct{}
	closed chan struct{}
	ended  chan struct{} // closed once the agent's output ends
	endOne sync.Once
	cancel context.CancelFunc // ends requests the agent sent
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
	p := &Process{cmd: cmd, hooks: hooks, done: make(chan struct{}), closed: make(chan struct{}), ended: make(chan struct{}), stderr: &tailBuffer{limit: 8192}, diag: &tailBuffer{limit: 4096}}
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
	// Requests the agent sends live as long as the process, not this startup.
	lifetime, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.Conn, err = acp.Connect(lifetime, transport{p}, handler{p})
	if err != nil {
		cancel()
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		killProcess(cmd)
		_ = cmd.Wait()
		return nil, fmt.Errorf("connect agent: %w", err)
	}
	go func() {
		_ = cmd.Wait()
		p.lifeMu.Lock()
		killProcess(cmd) // Terminate descendants left in this process group.
		close(p.done)
		p.lifeMu.Unlock()
	}()
	// Mu does not offer client file system or terminal methods: agents use
	// their own. ACP v2 removes both from the protocol. It renders plan
	// updates and boolean session options.
	info, err := p.Conn.Initialize(ctx, &acp.InitializeRequest{
		ProtocolVersion: ProtocolVersion,
		ClientInfo:      &acp.Implementation{Name: "mu", Version: "0.1.0"},
		ClientCapabilities: &acp.ClientCapabilities{
			Plan:    &acp.PlanCapabilities{},
			Session: &acp.ClientSessionCapabilities{ConfigOptions: &acp.SessionConfigOptionsCapabilities{Boolean: &acp.BooleanConfigOptionCapabilities{}}},
		},
	})
	if err == nil && info.ProtocolVersion != ProtocolVersion {
		err = fmt.Errorf("unsupported ACP version %d", info.ProtocolVersion)
	}
	if err == nil {
		err = RequireSessions(info.AgentCapabilities)
	}
	if err != nil {
		p.Close()
		return nil, p.Error(fmt.Errorf("initialize ACP: %w", err))
	}
	p.Info = *info
	return p, nil
}

// ProtocolVersion is the ACP version Mu speaks.
const ProtocolVersion acp.ProtocolVersion = 1

func (p *Process) Alive() bool {
	select {
	case <-p.closed:
		return false
	case <-p.ended:
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
	if output := p.diag.snapshot(); strings.TrimSpace(output) != "" {
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
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
		}
	})
}

func CancelledPermission() acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{Outcome: acp.CancelledRequestPermissionOutcome()}
}

// handler serves the requests an agent sends to Mu. The SDK answers methods
// it does not implement, such as the client file system and terminal
// methods, with method-not-found. Session updates are delivered in order by
// the transport instead.
type handler struct{ p *Process }

var _ acp.SessionRequestPermissionHandler = handler{}

func (h handler) SessionRequestPermission(ctx context.Context, req *acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	p := h.p
	if p.hooks.Permission == nil {
		response := CancelledPermission()
		return &response, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-p.closed:
		case <-p.ended:
		case <-ctx.Done():
		}
		cancel()
	}()
	response, err := p.hooks.Permission(ctx, *req)
	if err != nil {
		return nil, err
	}
	return &response, nil
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
