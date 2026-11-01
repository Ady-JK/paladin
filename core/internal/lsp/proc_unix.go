//go:build unix

package lsp

import (
	"os/exec"
	"syscall"
)

// killProcessGroup stops the server and anything it spawned.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
