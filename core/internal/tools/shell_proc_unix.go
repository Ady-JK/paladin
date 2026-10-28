//go:build unix

package tools

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

func spawnPiped(cmdStr, dir string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = dir
	cmd.Env = shellEnv("TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	outR, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	errR, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return cmd, outR, errR, nil
}

func spawnToLog(cmdStr, dir string, log *os.File) (*exec.Cmd, error) {
	dup, err := duplicateFile(log)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = dir
	cmd.Env = shellEnv("TERM=dumb", "NO_COLOR=1", "CLICOLOR=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func exitCode(err error) (*int, bool) {
	if err == nil {
		z := 0
		return &z, false
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return nil, true
		}
		c := ee.ExitCode()
		return &c, false
	}
	return nil, true
}
