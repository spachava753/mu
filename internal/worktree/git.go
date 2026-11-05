// Package worktree lists, plans, creates and removes a repository's git worktrees
// through the git and gh commands, with no UI or app state. Git runs with argument
// lists, never a shell. Everything here can wait on a process or the network, so
// nothing on the UI thread may call it.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/spachava753/mu/internal/procgroup"
	"github.com/spachava753/mu/internal/workspace"
)

const (
	readTimeout      = 10 * time.Second
	remoteTimeout    = 15 * time.Second
	fetchTimeout     = 2 * time.Minute
	checkoutTimeout  = 10 * time.Minute
	submoduleTimeout = 20 * time.Minute
)

// gitError is a git command that ran and failed.
type gitError struct {
	stderr string
	err    error
}

func (e *gitError) Unwrap() error { return e.err }
func (e *gitError) Error() string {
	if e.stderr != "" {
		return e.stderr
	}
	return e.err.Error()
}

// git runs a command in dir, bounded by timeout, and returns its trimmed output.
// It never waits for someone to type credentials.
func git(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	endWithGroup(cmd)
	cmd.Env = append(workspace.FolderEnv(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", &gitError{stderr: cleanMessage(stderr.String()), err: err}
		}
		return "", err
	}
	return strings.TrimRight(out.String(), "\r\n"), nil
}

// endWithGroup makes cancelling cmd also end what it started, such as ssh or a hook.
func endWithGroup(cmd *exec.Cmd) {
	procgroup.PrepareDetached(cmd)
	cmd.Cancel = func() error {
		procgroup.Kill(cmd)
		return nil
	}
	cmd.WaitDelay = time.Second
}

// settleGrace is how long a step that shouldn't be interrupted may go on after its
// context ends.
var settleGrace = 30 * time.Second

// settling is a context for a step that would leave the repository needing repair
// if it were killed partway: ending ctx only ends it after settleGrace.
func settling(ctx context.Context) (context.Context, context.CancelFunc) {
	settled, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, func() { time.AfterFunc(settleGrace, cancel) })
	return settled, func() {
		stop()
		cancel()
	}
}

// cleanMessage is the part of git's error output worth showing.
func cleanMessage(stderr string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if strings.HasPrefix(line, "hint:") || strings.HasPrefix(line, "Preparing worktree") || strings.HasPrefix(line, "HEAD is now at") {
			continue
		}
		line = strings.TrimPrefix(strings.TrimPrefix(line, "fatal: "), "error: ")
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// attempt runs a command whose failure only means no.
func attempt(ctx context.Context, dir string, args ...string) (string, bool) {
	out, err := git(ctx, readTimeout, dir, args...)
	return out, err == nil
}

// refExists reports whether a revision names a commit.
func refExists(ctx context.Context, dir, ref string) bool {
	_, ok := attempt(ctx, dir, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	return ok
}

func validBranchName(ctx context.Context, dir, branch string) bool {
	if branch == "" || branch == "HEAD" || strings.HasPrefix(branch, "-") {
		return false
	}
	_, ok := attempt(ctx, dir, "check-ref-format", "refs/heads/"+branch)
	return ok
}
