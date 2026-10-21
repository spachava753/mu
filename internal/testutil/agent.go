// Package testutil supplies an isolated deterministic stdio agent to tests.
package testutil

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func Agent(t *testing.T) string {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..")
	path := filepath.Join(t.TempDir(), "test-agent")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", path, "./internal/agent/testdata/agent")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test agent: %v\n%s", err, out)
	}
	return path
}
