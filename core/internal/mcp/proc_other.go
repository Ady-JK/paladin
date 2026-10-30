//go:build !unix

package mcp

import (
	"os/exec"
)

func setProcessGroup(*exec.Cmd) {}

func terminateGroup(cmd *exec.Cmd) { killGroup(cmd) }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
