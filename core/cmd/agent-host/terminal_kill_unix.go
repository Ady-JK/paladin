//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func killTerminalProcess(cmd *exec.Cmd) error {
	// pty.StartWithSize makes the shell a session leader. Kill its process
	// group as well so a foreground program does not outlive a closed tab.
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	return err
}
