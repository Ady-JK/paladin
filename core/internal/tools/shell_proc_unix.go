//go:build unix

package tools

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

func spawnToLog(cmdStr, dir string, log *os.File, sb *sandbox.Policy) (*exec.Cmd, error) {
	dup, err := duplicateFile(log)
	if err != nil {
		return nil, err
	}
	cmd := spawnShellCmd(cmdStr, dir, "TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	if err := confine(cmd, sb); err != nil {
		dup.Close()
		return nil, err
	}
	cmd.SysProcAttr = shellSysProcAttr()
	cmd.Stdout = log
	cmd.Stderr = dup
	if err := cmd.Start(); err != nil {
		dup.Close()
		return nil, err
	}
	return cmd, nil
}

func duplicateFile(f *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
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
