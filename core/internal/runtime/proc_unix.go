//go:build unix

package runtime

import (
	"errors"
	"syscall"
)

// groupAlive reports whether process group pid still has a member. A
// recycled pid can make this a false positive; never a false negative.
func groupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killGroup stops a whole orphaned process group: freeze first so a
// parent woken by a child's death cannot fork past the kill (the same
// discipline the shell tool uses for its own jobs).
func killGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGSTOP)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
