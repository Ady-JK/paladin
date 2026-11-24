//go:build !windows

package claude

import (
	"os"
	"syscall"
)

func syscallZero() os.Signal { return syscall.Signal(0) }
