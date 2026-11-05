//go:build !windows

// Package procgroup starts child processes in their own group, so that ending
// one also ends whatever it started.
package procgroup

import (
	"os/exec"
	"syscall"
)

// Prepare makes cmd the leader of a new process group.
func Prepare(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// Terminate asks cmd's group to exit.
func Terminate(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

// Kill ends cmd's group immediately.
func Kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
