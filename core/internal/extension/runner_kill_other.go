//go:build !unix

package extension

import "os"

func killRunnerProcess(p *os.Process) { _ = p.Kill() }
