//go:build !unix

package tools

import (
	"os"
	"os/exec"
)

func spawnToLog(cmdStr, dir string, log *os.File) (*exec.Cmd, error) {
	cmd := exec.Command("sh", "-c", cmdStr)
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
