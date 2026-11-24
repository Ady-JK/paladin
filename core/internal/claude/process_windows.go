package claude

import "os/exec"

func prepareProcess(_ *exec.Cmd)     {}
func terminateProcess(cmd *exec.Cmd) { killProcess(cmd) }
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
