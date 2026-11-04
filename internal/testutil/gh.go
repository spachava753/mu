package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// WithoutGH runs a package's tests with a signed-out stand-in first on PATH, so
// that nothing can reach GitHub through the real gh command, and returns the exit
// code. Call it from TestMain in packages whose tests can read a repository.
func WithoutGH(run func() int) int {
	dir, err := os.MkdirTemp("", "mu-test-gh")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	if err := writeGH(dir, "exit 4"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return run()
}

// FakeGH puts a gh that runs a shell script first on PATH for a test.
func FakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := writeGH(dir, script); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeGH(dir, script string) error {
	return os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o700)
}
