//go:build linux

package tools

import (
	"syscall"
	"testing"
)

func TestShellSysProcAttrHardensLinux(t *testing.T) {
	attr := shellSysProcAttr()
	if !attr.Setpgid {
		t.Error("commands must lead their own process group")
	}
	if attr.Pdeathsig != syscall.SIGKILL {
		t.Errorf("pdeathsig = %v, want SIGKILL so a dead core leaves no orphaned build", attr.Pdeathsig)
	}
}
