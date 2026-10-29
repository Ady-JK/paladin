//go:build !unix

package tools

import (
	"io"
	"os"
	"os/exec"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

func spawnPiped(cmdStr, dir string, sb *sandbox.Policy) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	if err := confine(cmd, sb); err != nil {
		return nil, nil, nil, err
	}
	cmd.Dir = dir
	cmd.Env = shellEnv("TERM=dumb")
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

func spawnToLog(cmdStr, dir string, log *os.File, sb *sandbox.Policy) (*exec.Cmd, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
	if err := confine(cmd, sb); err != nil {
		return nil, err
	}
	cmd.Dir = dir
	cmd.Env = shellEnv("TERM=dumb")
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
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func exitCode(err error) (*int, bool) {
	if err == nil {
		z := 0
		return &z, false
	}
	if ee, ok := err.(*exec.ExitError); ok {
		c := ee.ExitCode()
		return &c, false
	}
	return nil, true
}
