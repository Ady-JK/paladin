//go:build unix

package tools

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

func spawnPiped(cmdStr, dir string, sb *sandbox.Policy) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	cmd := spawnShellCmd(cmdStr, dir, "TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	if err := confine(cmd, sb); err != nil {
		return nil, nil, nil, err
	}
	cmd.SysProcAttr = shellSysProcAttr()
	outR, errR, err := startPiped(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	return cmd, outR, errR, nil
}

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

// shellNiceLevel is the scheduling priority model commands inherit.
// Agent builds and test suites want all available cores and would
// otherwise make the desktop (and the UI painting the results) stutter;
// nice lets the foreground app win the next timeslice.
const shellNiceLevel = "10"

// niceArgv prepends a best-effort `nice` so the whole command tree runs
// below the user's interactive work. Without a nice binary the command
// runs unchanged.
func niceArgv(argv []string) []string {
	nice, err := lookPath("nice")
	if err != nil || nice == "" {
		return argv
	}
	return append([]string{nice, "-n", shellNiceLevel}, argv...)
}

func exitCode(err error) (*int, bool) {
	if err == nil {
		z := 0
		return &z, false
	}
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return nil, true
		}
		c := ee.ExitCode()
		return &c, false
	}
	return nil, true
}
