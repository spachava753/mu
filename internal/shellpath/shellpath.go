// Package shellpath gives Mu the PATH of the user's login shell when macOS
// started it without one. Apps opened from the Dock or Finder get launchd's
// minimal PATH, which has none of the places agents, gh and Pi are installed.
package shellpath

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spachava753/mu/internal/procgroup"
)

// timeout bounds a shell whose startup files hang, which delays the window.
const timeout = 5 * time.Second

// marker sets the PATH apart from what startup files print.
const marker = "__MU_SHELL_PATH__"

// Adopt replaces PATH with the login shell's on macOS, unless Mu was started
// from a terminal, whose PATH is already the user's. It keeps PATH as it is on
// failure.
func Adopt() error {
	if runtime.GOOS != "darwin" || os.Getenv("TERM") != "" {
		return nil
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	path, err := read(ctx, shell)
	if err != nil {
		return fmt.Errorf("read PATH from %s: %w", shell, err)
	}
	return os.Setenv("PATH", path)
}

// read asks shell for its PATH as an interactive login shell, because version
// managers are commonly activated from the interactive startup file.
func read(ctx context.Context, shell string) (string, error) {
	script := fmt.Sprintf(`printf '%%s%%s%%s' '%s' "$PATH" '%s'`, marker, marker)
	cmd := exec.CommandContext(ctx, shell, "-ilc", script)
	procgroup.PrepareDetached(cmd)
	cmd.Cancel = func() error {
		procgroup.Kill(cmd)
		return nil
	}
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	parts := strings.Split(string(output), marker)
	if len(parts) != 3 || parts[1] == "" {
		return "", errors.New("no PATH in its output")
	}
	return parts[1], nil
}
