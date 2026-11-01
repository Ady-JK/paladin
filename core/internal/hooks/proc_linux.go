//go:build linux

package hooks

import (
	"os/exec"
	"syscall"
)

// setProcessGroup isolates the hook's tree and ties it to the core's
// lifetime: a crash or kill of agent-host must not leave a hook script
// running in the background.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
