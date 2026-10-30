// Package mcptest builds the mcp-dummy server once per test process.
package mcptest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	builtPath string
	buildErr  error
)

// Dummy returns the path of a freshly built mcp-dummy binary.
func Dummy(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "paladin-mcp-dummy-*")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "mcp-dummy")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, "./cmd/mcp-dummy")
		cmd.Dir = root
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildErr = &buildError{err: err, output: string(combined)}
			return
		}
		builtPath = out
	})
	if buildErr != nil {
		t.Fatalf("build mcp-dummy: %v", buildErr)
	}
	return builtPath
}

type buildError struct {
	err    error
	output string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.output }

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
