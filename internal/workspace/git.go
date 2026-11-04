// Package workspace reads facts about a thread's folder from local tools.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Git describes the repository a folder is in. It is empty outside a repository.
type Git struct {
	Branch   string // the checked-out branch, unless HEAD is detached
	Commit   string // the short commit, only when HEAD is detached
	Worktree string // the path of a linked worktree; empty in the main one
}

// Repo reports whether the folder is in a repository.
func (g Git) Repo() bool { return g != Git{} }

// InspectGit reads the folder's repository. A folder outside any repository, a
// missing folder, or a machine without git is no error and reads as empty. An
// error means the answer is unknown, as when ctx ended or git cannot read the
// repository's configuration.
func InspectGit(ctx context.Context, dir string) (Git, error) {
	out, err := git(ctx, dir, "rev-parse", "--absolute-git-dir", "--git-common-dir", "--show-toplevel")
	if err != nil {
		if noRepository(err) {
			return Git{}, nil
		}
		return Git{}, err
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		return Git{}, fmt.Errorf("unexpected output from git rev-parse: %q", out)
	}
	var g Git
	common := lines[1]
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	if !samePath(lines[0], common) {
		g.Worktree = lines[2]
	}
	g.Branch, err = git(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err == nil {
		return g, nil
	}
	if exit := (*exec.ExitError)(nil); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return Git{}, err // With -q, only a detached HEAD exits with 1.
	}
	g.Commit, err = git(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return Git{}, err
	}
	return g, nil
}

// noRepository reports whether a failure means there is no repository to read,
// rather than one that could not be read. Inside a .git folder or a bare
// repository there is no working tree, which is the same to the user.
func noRepository(err error) bool {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return true
	}
	var failed *gitError
	return errors.As(err, &failed) && (strings.Contains(failed.stderr, "not a git repository") || strings.Contains(failed.stderr, "must be run in a work tree"))
}

// gitError is a git command that ran and failed.
type gitError struct {
	*exec.ExitError
	stderr string
}

func (e *gitError) Error() string { return e.ExitError.Error() + ": " + e.stderr }
func (e *gitError) Unwrap() error { return e.ExitError }

func samePath(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return a == b
}

// folderEnv is the environment without the variables that would point a command
// at another repository than the folder it runs in.
func folderEnv() []string {
	var env []string
	for _, v := range os.Environ() {
		name, _, _ := strings.Cut(v, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GH_REPO":
		default:
			env = append(env, v)
		}
	}
	return env
}

// git runs a read-only command in dir and returns its output. It takes no
// optional locks, so it cannot get in the way of the agent's own git commands.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	cmd.Env = append(folderEnv(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C") // noRepository reads its messages.
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", &gitError{exit, strings.TrimSpace(stderr.String())}
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}
