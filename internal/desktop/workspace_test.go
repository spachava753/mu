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

func TestPullRequestLink(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/o/r/pull/1": "https://github.com/o/r/pull/1",
		"http://ghe.example/o/r/pull/1": "http://ghe.example/o/r/pull/1",
		"":                              "",
		"file:///etc/passwd":            "",
		"javascript:alert(1)":           "",
		"-https://github.com/":          "",
	} {
		if got := pullRequestLink(workspace.PullRequest{URL: url}); got != want {
			t.Errorf("%q: %q, want %q", url, got, want)
		}
	}
}
