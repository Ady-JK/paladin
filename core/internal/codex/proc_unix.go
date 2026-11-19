//go:build unix && !linux

package codex

import (
	"os/exec"
	"syscall"
)

// setProcAttr gives the app-server its own process group so the whole tree
// can be signalled at once.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
