//go:build unix

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

func spawnToLog(cmdStr, dir string, log *os.File) (*exec.Cmd, error) {
	cmd := spawnShellCmd(cmdStr, dir, "TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	cmd.SysProcAttr = shellSysProcAttr()
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func killProcessGroup(pid int) {
	if pid == 0 {
		return
	}
	// Freeze first: macOS signals members one by one, so a parent woken by
	// its child's death could fork the next command past the SIGKILL.
	_ = syscall.Kill(-pid, syscall.SIGSTOP)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
