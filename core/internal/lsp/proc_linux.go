//go:build linux

package lsp

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the server in its own group so stopping it also
// stops anything it spawned, and ties it to the core's lifetime so a
// crashed agent-host does not leave a language server behind.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
