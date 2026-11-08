//go:build !unix

package extension

import "syscall"

func runnerSysProcAttr() *syscall.SysProcAttr { return nil }
