//go:build unix && !linux

package extension

import "syscall"

// runnerSysProcAttr leaves the runner's lifetime to the pool: reapIdle
// and Close stop it everywhere else.
func runnerSysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
