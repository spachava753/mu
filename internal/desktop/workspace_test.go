package desktop

import (
	"testing"

	"github.com/spachava753/mu/internal/workspace"
)

func TestPullRequestState(t *testing.T) {
	for _, tt := range []struct {
		pr   workspace.PullRequest
		want string
	}{
		{workspace.PullRequest{State: "OPEN"}, "Open"},
		{workspace.PullRequest{State: "OPEN", Draft: true}, "Draft"},
		{workspace.PullRequest{State: "MERGED"}, "Merged"},
		{workspace.PullRequest{State: "CLOSED"}, "Closed"},
		{workspace.PullRequest{State: "CLOSED", Draft: true}, "Closed"},
		{workspace.PullRequest{State: "NEW"}, "NEW"},
	} {
		if got := pullRequestState(tt.pr); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.pr, got, tt.want)
		}
	}
}
