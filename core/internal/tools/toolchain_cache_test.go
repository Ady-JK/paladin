package tools

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Every shell, MCP and LSP spawn builds a PATH; the dirs behind it change
// rarely, so the filesystem is probed once per short window, not per spawn.
func TestToolchainDirsAreProbedOncePerWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()
	prev := toolchainClock
	toolchainClock = func() time.Time { return now }
	t.Cleanup(func() { toolchainClock = prev })

	bin := filepath.Join(home, ".cargo", "bin")
	if slices.Contains(toolchainDirs(), bin) {
		t.Fatal("the dir does not exist yet")
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(toolchainDirs(), bin) {
		t.Fatal("a second call inside the window probed the filesystem again")
	}
	now = now.Add(toolchainTTL + time.Second)
	if !slices.Contains(toolchainDirs(), bin) {
		t.Fatal("a new toolchain dir was not seen after the window")
	}
}

func TestToolchainDirsCacheIsPerHome(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(b, ".cargo", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", a)
	_ = toolchainDirs()
	t.Setenv("HOME", b)
	if !slices.Contains(toolchainDirs(), filepath.Join(b, ".cargo", "bin")) {
		t.Fatal("another home reused the first home's cached answer")
	}
}
