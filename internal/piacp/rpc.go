package piacp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/spachava753/mu/internal/procgroup"
)

// Grace periods for Pi to exit after its input closes, and then after SIGTERM.
// It normally exits at once, and ends its running tools when it does.
const (
	exitGrace      = 2 * time.Second
	terminateGrace = time.Second
)

// rejected is a command Pi answered with success: false.
type rejected struct{ message string }

func (e *rejected) Error() string { return e.message }

// piProcess is one `pi --mode rpc` child, which holds one session.
//
// A single goroutine handles its output in order, responses included, so a
// command's response is never seen before the events Pi wrote ahead of it, and
// Pi is always being read however slow the handler is. The event handler must
// not wait for a command, since that response would queue behind it.
type piProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	stderr  *tail
	onEvent func(eventType string, raw []byte)

	mu      sync.Mutex
	next    int
	pending map[string]chan response
	queue   [][]byte
	wake    chan struct{}
	eof     bool

	exited    chan struct{}
	closeOnce sync.Once
}

type response struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

// startPi starts Pi in rpc mode. Pi prints nothing until asked, so callers treat
// the first response as the sign that it's ready.
func startPi(executable, cwd string, extra []string, onEvent func(string, []byte)) (*piProcess, error) {
	cmd := exec.Command(executable, append([]string{"--mode", "rpc"}, extra...)...)
	cmd.Dir = cwd
	procgroup.Prepare(cmd)
	p := &piProcess{cmd: cmd, stderr: &tail{limit: 4096}, onEvent: onEvent, pending: map[string]chan response{}, wake: make(chan struct{}, 1), exited: make(chan struct{})}
	cmd.Stderr = p.stderr
	var err error
	if p.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go p.read(stdout)
	go p.pump()
	return p, nil
}

// read splits Pi's output on LF only, so that U+2028 in a string can't end a
// record, without bufio.Scanner's limit: records over 200 KB are ordinary.
func (p *piProcess) read(stdout io.Reader) {
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if line = bytes.TrimRight(line, "\r\n"); len(line) > 0 {
			p.mu.Lock()
			p.queue = append(p.queue, line)
			p.mu.Unlock()
			p.signal()
		}
		if err != nil {
			p.mu.Lock()
			p.eof = true
			p.mu.Unlock()
			p.signal()
			return
		}
	}
}

func (p *piProcess) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *piProcess) pump() {
	defer close(p.exited)
	defer func() { _ = p.cmd.Wait() }()
	for {
		p.mu.Lock()
		batch, eof := p.queue, p.eof
		p.queue = nil
		p.mu.Unlock()
		for _, line := range batch {
			p.handle(line)
		}
		if len(batch) > 0 {
			continue
		}
		if eof {
			return
		}
		<-p.wake
	}
}

func (p *piProcess) handle(line []byte) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
		// Not protocol. Pi prints a question here in some modes of failure.
		fmt.Fprintf(p.stderr, "%s\n", line)
		return
	}
	if head.Type != "response" {
		p.onEvent(head.Type, line)
		return
	}
	var r response
	if json.Unmarshal(line, &r) != nil {
		return
	}
	p.mu.Lock()
	waiter := p.pending[r.ID]
	delete(p.pending, r.ID)
	p.mu.Unlock()
	if waiter != nil {
		waiter <- r
	}
}

// call sends a command and waits for its response, which it decodes into out.
func (p *piProcess) call(ctx context.Context, kind string, fields map[string]any, out any) error {
	p.mu.Lock()
	p.next++
	id := fmt.Sprintf("mu-%d", p.next)
	waiter := make(chan response, 1)
	p.pending[id] = waiter
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	command := map[string]any{"id": id, "type": kind}
	for k, v := range fields {
		command[k] = v
	}
	if err := p.write(command); err != nil {
		return p.exitError(err)
	}
	select {
	case r := <-waiter:
		if !r.Success {
			return &rejected{message: r.Error}
		}
		if out == nil || len(r.Data) == 0 {
			return nil
		}
		return json.Unmarshal(r.Data, out)
	case <-p.exited:
		return p.exitError(errors.New("Pi exited"))
	case <-ctx.Done():
		return p.exitError(fmt.Errorf("Pi did not answer %s: %w", kind, ctx.Err()))
	}
}

func (p *piProcess) write(record map[string]any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

// exitError adds what Pi said on stderr, which is usually the actual reason.
func (p *piProcess) exitError(err error) error {
	if output := strings.TrimSpace(p.stderr.String()); output != "" {
		return fmt.Errorf("%w: %s", err, output)
	}
	return err
}

func (p *piProcess) alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// close ends Pi: its input closes, then it's asked to terminate, then killed.
// Pi saves as it goes and ends its running tools itself.
func (p *piProcess) close() {
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		exited := func(grace time.Duration) bool {
			select {
			case <-p.exited:
				return true
			case <-time.After(grace):
				return false
			}
		}
		if exited(exitGrace) {
			return
		}
		procgroup.Terminate(p.cmd)
		if exited(terminateGrace) {
			return
		}
		procgroup.Kill(p.cmd)
		exited(exitGrace) // Something outside the group may hold its output open.
	})
}

// kill ends Pi at once, for when the connection to Mu has been cut.
func (p *piProcess) kill() {
	procgroup.Kill(p.cmd)
	p.close()
}

// tail keeps the end of Pi's stderr for error reports.
type tail struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, b...)
	if len(t.data) > t.limit {
		t.data = append(t.data[:0], t.data[len(t.data)-t.limit:]...)
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.data), "�")
}
