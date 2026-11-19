//go:build unix

package codex

import (
	"os/exec"
	"syscall"
)

// killGroup stops the app-server and everything it started.
func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
