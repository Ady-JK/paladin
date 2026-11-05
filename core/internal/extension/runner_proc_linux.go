//go:build linux

package extension

import "syscall"

// runnerSysProcAttr ties the runner's lifetime to the core: if agent-host
// dies, the kernel kills the plugin process instead of leaving an
// orphaned Node behind. Each runner also owns its output.
func runnerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
