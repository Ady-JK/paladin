//go:build unix

package hooks

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup freezes the group before killing it: macOS signals members
// one by one, so a parent woken by its child's death could otherwise
// fork the script's next command past the SIGKILL.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGSTOP)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
