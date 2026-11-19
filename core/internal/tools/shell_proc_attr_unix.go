//go:build unix && !linux

package tools

import "syscall"

// shellSysProcAttr starts every command in a session of its own. That makes
// it the leader of its process group, so the whole tree can be signalled at
// once, and leaves it without a controlling terminal: an interactive shell in
// a background group of the user's terminal is stopped by the kernel as soon
// as it touches the tty. The pdeathsig equivalent is Linux-only; elsewhere
// the core closes pipes and kills the group when it shuts down.
func shellSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
