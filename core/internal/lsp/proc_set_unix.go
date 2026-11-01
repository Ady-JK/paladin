//go:build unix && !linux

package lsp

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the server in its own group so stopping it also
// stops anything it spawned.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
