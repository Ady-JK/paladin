//go:build !linux && !windows

package claude

import "os/exec"

func parentDeath(_ *exec.Cmd) {}
