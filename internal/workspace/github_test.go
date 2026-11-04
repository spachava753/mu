package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spachava753/mu/internal/testutil"
)

func TestInspectPullRequest(t *testing.T) {
	for _, tt := range []struct {
		name, reply string
		want        PullRequest
	}{
		{"open", `{"number":12,"state":"OPEN","isDraft":false,"title":"Fix"}`, PullRequest{Number: 12, State: "OPEN", Title: "Fix"}},
		{"draft", `{"number":13,"state":"OPEN","isDraft":true,"title":"Wip"}`, PullRequest{Number: 13, State: "OPEN", Draft: true, Title: "Wip"}},
		{"merged", `{"number":14,"state":"MERGED","isDraft":false,"title":"Done"}`, PullRequest{Number: 14, State: "MERGED", Title: "Done"}},
		{"closed", `{"number":15,"state":"CLOSED","isDraft":false,"title":"No"}`, PullRequest{Number: 15, State: "CLOSED", Title: "No"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testutil.FakeGH(t, "echo '"+tt.reply+"'")
			got, err := InspectPullRequest(context.Background(), t.TempDir())
			if err != nil || got != tt.want || !got.Found() {
				t.Fatalf("%+v, %v", got, err)
			}
		})
	}
}

func TestInspectNoPullRequest(t *testing.T) {
	for _, tt := range []struct{ name, script string }{
		{"none", `echo 'no pull requests found for branch "main"' >&2; exit 1`},
		{"no remotes", `echo 'no git remotes found' >&2; exit 1`},
		{"other host", `echo 'none of the git remotes configured for this repository point to a known GitHub host' >&2; exit 1`},
		{"no repository", `echo 'failed to run git: fatal: not a git repository' >&2; exit 1`},
		{"signed out", `echo 'To get started with GitHub CLI, please run:  gh auth login' >&2; exit 4`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testutil.FakeGH(t, tt.script)
			got, err := InspectPullRequest(context.Background(), t.TempDir())
			if err != nil || got.Found() {
				t.Fatalf("%+v, %v", got, err)
			}
		})
	}
}

func TestInspectPullRequestWithoutGH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got, err := InspectPullRequest(context.Background(), t.TempDir())
	if err != nil || got.Found() {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestInspectPullRequestMissingFolder(t *testing.T) {
	testutil.FakeGH(t, `echo '{"number":1}'`)
	got, err := InspectPullRequest(context.Background(), filepath.Join(t.TempDir(), "gone"))
	if err != nil || got.Found() {
		t.Fatalf("%+v, %v", got, err)
	}
}

// Failing to find out is an unknown answer, not the absence of a pull request.
func TestInspectPullRequestUnknown(t *testing.T) {
	for _, tt := range []struct{ name, script string }{
		{"network", `echo 'error connecting to api.github.com' >&2; exit 1`},
		{"bad output", `echo 'not json'`},
		{"branch unreadable", `echo 'could not determine current branch: failed to run git: input/output error' >&2; exit 1`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testutil.FakeGH(t, tt.script)
			if got, err := InspectPullRequest(context.Background(), t.TempDir()); err == nil {
				t.Fatalf("read as %+v", got)
			}
		})
	}
	testutil.FakeGH(t, `echo '{"number":1}'`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectPullRequest(ctx, t.TempDir()); err == nil {
		t.Fatal("an ended context is an unknown answer")
	}
}

// gh runs in the folder, unprompted, for that folder's repository.
func TestInspectPullRequestEnvironment(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	t.Setenv("GH_REPO", "someone/else")
	t.Setenv("GIT_DIR", "/somewhere/else/.git")
	testutil.FakeGH(t, `{ pwd; echo "$*"; echo "repo=$GH_REPO gitdir=$GIT_DIR prompt=$GH_PROMPT_DISABLED notifier=$GH_NO_UPDATE_NOTIFIER"; } > '`+log+`'; echo '{"number":1,"state":"OPEN"}'`)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPullRequest(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{dir + "\n", "pr view --json number,state,isDraft,title\n", "repo= gitdir= prompt=1 notifier=1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
