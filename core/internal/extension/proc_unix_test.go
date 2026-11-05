//go:build unix

package extension

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// killProcess sends SIGKILL to a runner, standing in for a crash.
func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGKILL)
}

// processAlive reports whether pid is still running. A zombie counts as
// gone: the kernel keeps the entry until it is reaped.
func processAlive(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

func alive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH)
}
