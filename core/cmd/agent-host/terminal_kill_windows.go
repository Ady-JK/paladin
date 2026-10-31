//go:build windows

package main

import "os/exec"

func killTerminalProcess(cmd *exec.Cmd) error { return cmd.Process.Kill() }
