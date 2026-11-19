//go:build unix

package tools

import "testing"

// An interactive zsh in a background process group of the user's terminal is
// stopped by the kernel when it touches the tty, which hung every command when
// the app was started from a terminal. A session of its own has no controlling
// terminal, and still makes the command its group leader so the tree can be
// signalled at once.
func TestShellCommandsRunWithoutAControllingTerminal(t *testing.T) {
	if !shellSysProcAttr().Setsid {
		t.Fatal("commands must start their own session so they never contend for the user's terminal")
	}
}
