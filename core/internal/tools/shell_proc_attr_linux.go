//go:build linux

package tools

import "syscall"

// shellSysProcAttr starts every command in a session of its own. That makes
// it the leader of its process group, so the whole tree can be signalled at
// once, and leaves it without a controlling terminal: an interactive shell in
// a background group of the user's terminal is stopped by the kernel as soon
// as it touches the tty. It also ties its lifetime to the core: if agent-host
// dies or is killed, the kernel sends SIGKILL to the command instead of
// leaving an orphaned build or dev server burning CPU for the rest of the
// session.
func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
}
