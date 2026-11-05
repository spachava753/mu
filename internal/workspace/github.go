package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PullRequest is the pull request of a folder's current branch.
type PullRequest struct {
	Number int
	State  string // OPEN, CLOSED or MERGED, as GitHub reports it
	Draft  bool
	Title  string
	URL    string
}

// Found reports whether the branch has a pull request.
func (p PullRequest) Found() bool { return p.Number != 0 }

// InspectPullRequest asks the gh command about the folder's current branch,
// which contacts GitHub with the user's own credentials. No gh, no sign-in, no
// GitHub remote and no pull request are no errors and read as empty. An error
// means the answer is unknown, as when GitHub cannot be reached.
func InspectPullRequest(ctx context.Context, dir string) (PullRequest, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return PullRequest{}, nil
	}
	cmd := exec.CommandContext(ctx, path, "pr", "view", "--json", "number,state,isDraft,title,url")
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	cmd.Env = append(FolderEnv(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "LC_ALL=C")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return PullRequest{}, ctx.Err()
		}
		if noPullRequest(err, stderr.String()) {
			return PullRequest{}, nil
		}
		return PullRequest{}, fmt.Errorf("gh pr view: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var reply struct {
		Number  int    `json:"number"`
		State   string `json:"state"`
		IsDraft bool   `json:"isDraft"`
		Title   string `json:"title"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		return PullRequest{}, fmt.Errorf("gh pr view: %w", err)
	}
	return PullRequest{Number: reply.Number, State: reply.State, Draft: reply.IsDraft, Title: reply.Title, URL: reply.URL}, nil
}

// noPullRequest reports whether gh's failure means there is nothing to show,
// rather than that it could not find out.
func noPullRequest(err error, stderr string) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true // The folder is gone.
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 4 {
		return true // gh's code for needing to sign in.
	}
	stderr = strings.ToLower(stderr)
	for _, reason := range []string{"no pull requests found", "no git remotes found", "none of the git remotes", "not a git repository"} {
		if strings.Contains(stderr, reason) {
			return true
		}
	}
	return false
}
