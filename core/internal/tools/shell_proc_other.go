//go:build !unix

package tools

import (
	"os"
	"os/exec"

	"github.com/Ady-JK/paladin/core/internal/sandbox"
)

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
