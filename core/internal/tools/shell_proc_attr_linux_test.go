//go:build linux

package tools

import (
	"syscall"
	"testing"
)

func TestShellSysProcAttrHardensLinux(t *testing.T) {
	attr := shellSysProcAttr()
	if !attr.Setsid {
		t.Error("commands must lead their own session, and so their own process group")
	}
	if attr.Pdeathsig != syscall.SIGKILL {
		t.Errorf("pdeathsig = %v, want SIGKILL so a dead core leaves no orphaned build", attr.Pdeathsig)
	}
}
