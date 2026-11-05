//go:build windows

// Package procgroup starts child processes in their own group, so that ending
// one also ends whatever it started. On Windows only the direct child is ended.
package procgroup

import "os/exec"

// Prepare does nothing on Windows.
func Prepare(*exec.Cmd) {}

// PrepareDetached does nothing on Windows.
func PrepareDetached(*exec.Cmd) {}

// Terminate does nothing: Windows has no SIGTERM for console processes, so
// callers go on to Kill.
func Terminate(*exec.Cmd) {}

// Kill ends cmd immediately.
func Kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
