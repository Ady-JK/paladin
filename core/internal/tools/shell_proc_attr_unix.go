//go:build unix && !linux

package tools

import "syscall"

// shellSysProcAttr puts every command in its own process group. The
// pdeathsig equivalent is Linux-only; elsewhere the core closes pipes
// and kills the group when it shuts down.
func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
