//go:build darwin || linux

package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestShellLogClosesAllParentDescriptors(t *testing.T) {
	// A leaked *os.File can otherwise be closed by the garbage collector,
	// masking descriptor growth across commands in a long-lived sidecar.
	path := filepath.Join(t.TempDir(), "command.log")
	log, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	var info syscall.Stat_t
	if err := syscall.Fstat(int(log.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	// Reserve and release the next free descriptor, which a duplicate of
	// the log would occupy. /dev/fd only lists standard streams on macOS.
	probe, err := syscall.Dup(int(log.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Close(probe); err != nil {
		t.Fatal(err)
	}
	cmd, err := spawnToLog("printf stdout; printf stderr >&2", filepath.Dir(path), log)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "stdoutstderr" {
		t.Fatalf("combined log = %q, %v", data, err)
	}
	var open syscall.Stat_t
	err = syscall.Fstat(probe, &open)
	runtime.KeepAlive(cmd)
	if err == nil && open.Dev == info.Dev && open.Ino == info.Ino {
		t.Fatal("finished shell command leaked a log descriptor in the parent")
	}
}
