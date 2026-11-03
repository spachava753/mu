package agent

import "os/exec"

func prepareProcess(cmd *exec.Cmd) {}

// Windows has no SIGTERM for console processes; Close goes on to kill.
func terminateProcess(cmd *exec.Cmd) {}
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
