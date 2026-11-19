//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachFromTerminal starts the command in a session of its own. Run from a
// terminal, an interactive shell sharing the sidecar's controlling tty fights
// it for the foreground and can be stopped by the kernel.
func detachFromTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
