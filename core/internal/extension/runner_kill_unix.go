//go:build unix

package extension

import (
	"os"
	"syscall"
)

func killRunnerProcess(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	_ = p.Kill()
}
