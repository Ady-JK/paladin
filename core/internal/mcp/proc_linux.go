//go:build linux

package mcp

import (
	"os/exec"
	"syscall"
)

// setProcessGroup isolates the server and ties it to the core's
// lifetime; a crashed or killed agent-host must not leave npx/uvx or a
// language server running forever.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
