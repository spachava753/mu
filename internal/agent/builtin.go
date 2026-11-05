package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spachava753/acp-sdk/acp"
)

// Server is an ACP agent that runs inside Mu. It serves link.Transport until the
// transport closes or ctx ends, and must have stopped everything it started by the
// time it returns. To the rest of Mu it is a Process like any other.
type Server func(ctx context.Context, link Link) error

// Link is what a Server is given in place of a process's standard streams, working
// directory and standard error.
type Link struct {
	Transport acp.Transport
	Cwd       string
	Diag      io.Writer
}

// builtinPrefix marks a command as naming a Server rather than an executable, such
// as "mu:pi". It is saved as any other command is, so a thread's command snapshot
// and connection key work unchanged.
const builtinPrefix = "mu:"

// BuiltinCommand is the command that names a built-in agent.
func BuiltinCommand(name string) string { return builtinPrefix + name }

// BuiltinName reports which built-in agent a command names, if any.
func BuiltinName(command string) (string, bool) {
	return strings.CutPrefix(command, builtinPrefix)
}

// Serve runs server inside Mu over a pair of pipes, so that the ACP connection
// behaves as it does with a process, including a write blocked on a full pipe. Its
// "exit" is the server returning. Terminating it cancels it, and killing it also
// closes its end of the pipes, so Close's escalation applies unchanged.
func Serve(ctx context.Context, server Server, cwd string, hooks Hooks) (*Process, error) {
	p := newProcess(hooks)
	input, stdin, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdout, output, err := os.Pipe()
	if err != nil {
		_ = input.Close()
		_ = stdin.Close()
		return nil, err
	}
	p.stdin, p.stdout = stdin, stdout
	serverCtx, cancel := context.WithCancel(context.Background())
	release := func() {
		_ = input.Close()
		_ = output.Close()
	}
	p.terminate = cancel
	p.forceKill = func() {
		cancel()
		release()
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer cancel()
		defer release()
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(p.stderr, "built-in agent panicked: %v\n", r)
			}
		}()
		link := Link{Transport: &acp.IOTransport{Reader: input, Writer: output}, Cwd: cwd, Diag: p.stderr}
		if err := server(serverCtx, link); err != nil && serverCtx.Err() == nil {
			fmt.Fprintln(p.stderr, err)
		}
	}()
	return p.connect(ctx, func() { <-finished })
}
