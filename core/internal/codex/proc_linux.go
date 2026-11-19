//go:build linux

package codex

import (
	"os/exec"
	"syscall"
)

// setProcAttr gives the app-server its own process group and makes the
// kernel kill it if Paladin dies.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
