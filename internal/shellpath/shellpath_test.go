//go:build !windows

package shellpath

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeShell writes an executable that runs body and is given the arguments
// Mu passes a shell.
func fakeShell(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shell")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadIgnoresWhatStartupFilesPrint(t *testing.T) {
	shell := fakeShell(t, `echo "Welcome back"; PATH=/from/shell:/usr/bin; export PATH; /bin/sh -c "$2"; echo "Bye"`)
	path, err := read(context.Background(), shell)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/from/shell:/usr/bin" {
		t.Fatalf("path = %q", path)
	}
}

func TestReadFails(t *testing.T) {
	for name, body := range map[string]string{
		"exit status": "exit 3",
		"no marker":   `echo "$PATH"`,
		"empty PATH":  `PATH= /bin/sh -c "$2"`,
	} {
		t.Run(name, func(t *testing.T) {
			if path, err := read(context.Background(), fakeShell(t, body)); err == nil {
				t.Fatalf("path = %q, want an error", path)
			}
		})
	}
}

func TestReadEndsAShellThatHangs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The child holds the output open, so the shell's own exit isn't enough.
	if _, err := read(ctx, fakeShell(t, "sleep 30 & wait")); err == nil {
		t.Fatal("want an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %v", elapsed)
	}
}

func TestAdoptLeavesATerminalsPathAlone(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("PATH", "/from/terminal")
	t.Setenv("SHELL", fakeShell(t, "exit 3"))
	if err := Adopt(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PATH"); got != "/from/terminal" {
		t.Fatalf("PATH = %q", got)
	}
}
