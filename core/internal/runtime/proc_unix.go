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
