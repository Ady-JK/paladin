//go:build linux

package tools

import "syscall"

// shellSysProcAttr puts every command in its own process group, so the
// whole tree can be signalled at once, and ties its lifetime to the
// core: if agent-host dies or is killed, the kernel sends SIGKILL to
// the command instead of leaving an orphaned build or dev server
// burning CPU for the rest of the session.
func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
