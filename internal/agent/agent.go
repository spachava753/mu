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
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
	"github.com/google/shlex"
)

type Hooks struct {
	Terminal   func(acp.SessionId, string, string)
	Update     func(acp.SessionNotification)
	Permission func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

type Process struct {
	Conn      *acp.ClientSideConnection
	Info      acp.InitializeResponse
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    *tailBuffer
	done      chan struct{}
	closed    chan struct{}
	once      sync.Once
	lifeMu    sync.Mutex
	hooks     Hooks
	cwd       string
	mu        sync.Mutex
	terminals map[string]*terminal
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
	p := &Process{cmd: cmd, hooks: hooks, cwd: cwd, done: make(chan struct{}), closed: make(chan struct{}), stderr: &tailBuffer{limit: 8192}, terminals: map[string]*terminal{}}
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
	p.Conn = acp.NewClientSideConnection(p, p.stdin, p.stdout)
	p.Conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	go func() {
		_ = cmd.Wait()
		p.lifeMu.Lock()
		killProcess(cmd) // Terminate descendants left in this process group.
		close(p.done)
		p.lifeMu.Unlock()
	}()
	info, err := p.Conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientInfo:         &acp.Implementation{Name: "mu", Version: "0.1.0"},
		ClientCapabilities: acp.ClientCapabilities{Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true}, Terminal: true},
	})
	if err == nil && info.ProtocolVersion != acp.ProtocolVersionNumber {
		err = fmt.Errorf("unsupported ACP version %d", info.ProtocolVersion)
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
	output, _ := p.stderr.snapshot()
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("%w\nAgent stderr:\n%s", err, output)
	}
	return err
}

func (p *Process) Close() {
	p.once.Do(func() {
		close(p.closed)
		p.mu.Lock()
		var terminals []*terminal
		for _, t := range p.terminals {
			t.kill()
			terminals = append(terminals, t)
		}
		p.mu.Unlock()
		_ = p.stdin.Close()
		p.lifeMu.Lock()
		select {
		case <-p.done:
		default:
			killProcess(p.cmd)
		}
		p.lifeMu.Unlock()
		_ = p.stdout.Close()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		select {
		case <-p.done:
		case <-deadline.C:
			return
		}
		for _, t := range terminals {
			select {
			case <-t.done:
			case <-deadline.C:
				return
			}
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

// Client file access is restricted to the chosen workspace, including symlinks.
// This is not a sandbox: the agent executable and its commands run as the user.
func (p *Process) rootPath(path string) (*os.Root, string, error) {
	if !filepath.IsAbs(path) {
		return nil, "", errors.New("ACP file paths must be absolute")
	}
	rel, err := filepath.Rel(p.cwd, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, "", errors.New("file is outside this thread's working directory")
	}
	root, err := os.OpenRoot(p.cwd)
	return root, rel, err
}

func (p *Process) ReadTextFile(_ context.Context, req acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	root, rel, err := p.rootPath(req.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	if len(b) > 8*1024*1024 {
		return acp.ReadTextFileResponse{}, errors.New("text file exceeds 8 MiB")
	}
	if !utf8.Valid(b) {
		return acp.ReadTextFileResponse{}, errors.New("file is not UTF-8 text")
	}
	text := string(b)
	if req.Line != nil || req.Limit != nil {
		lines := strings.SplitAfter(text, "\n")
		start := 0
		if req.Line != nil {
			start = *req.Line - 1
		}
		if start < 0 || (req.Limit != nil && *req.Limit < 0) {
			return acp.ReadTextFileResponse{}, errors.New("invalid line range")
		}
		start = min(start, len(lines))
		end := len(lines)
		if req.Limit != nil {
			end = start + min(*req.Limit, len(lines)-start)
		}
		text = strings.Join(lines[start:end], "")
	}
	return acp.ReadTextFileResponse{Content: text}, nil
}

func (p *Process) WriteTextFile(_ context.Context, req acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	root, rel, err := p.rootPath(req.Path)
	if err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	defer root.Close()
	if len(req.Content) > 8*1024*1024 {
		return acp.WriteTextFileResponse{}, errors.New("text file exceeds 8 MiB")
	}
	if err = root.MkdirAll(filepath.Dir(rel), 0755); err == nil {
		err = root.WriteFile(rel, []byte(req.Content), 0644)
	}
	return acp.WriteTextFileResponse{}, err
}

// tailBuffer bounds retained process output and is safe for concurrent stdout/stderr.
type tailBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
	onWrite   func()
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer func() {
		b.mu.Unlock()
		if b.onWrite != nil {
			b.onWrite()
		}
	}()
	n := len(p)
	if len(p) >= b.limit {
		b.truncated = b.truncated || len(b.data)+len(p) > b.limit
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
	} else {
		b.data = append(b.data, p...)
		if len(b.data) > b.limit {
			b.data = append(b.data[:0], b.data[len(b.data)-b.limit:]...)
			b.truncated = true
		}
	}
	return n, nil
}
func (b *tailBuffer) snapshot() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.data
	for len(d) > 0 && !utf8.RuneStart(d[0]) {
		d = d[1:]
	}
	return strings.ToValidUTF8(string(d), "�"), b.truncated
}
