//go:build !unix

package codex

import "os/exec"

func setProcAttr(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
