// Package sandboxtest gives tests a real sandbox helper binary.
//
// The Linux sandbox confines a command by re-execing its own executable
// with sandbox.HelperArg, which only agent-host implements. Under
// `go test` that executable is the test binary, so a re-exec would run
// the whole suite again inside itself — recursively, until the timeout
// or the OOM killer stops it. Tests therefore build agent-host once and
// point the helper override at it.
//
// This package deliberately does not import the sandbox package: the
// sandbox package's own tests use it, and importing back would be an
// import cycle. Callers skip when their platform has no OS sandbox.
package sandboxtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// HelperEnv must match sandbox.HelperEnv.
const HelperEnv = "PALADIN_SANDBOX_HELPER"

var (
	buildOnce sync.Once
	builtPath string
	buildErr  error
)

// Helper builds agent-host once per test process, points the helper
// override at it, and returns its path.
func Helper(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "paladin-sandbox-helper-*")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "agent-host")
		cmd := exec.Command("go", "build", "-o", out, "./cmd/agent-host")
		cmd.Dir = root
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildErr = &buildError{err: err, output: string(combined)}
			return
		}
		builtPath = out
	})
	if buildErr != nil {
		t.Fatalf("build sandbox helper: %v", buildErr)
	}
	t.Setenv(HelperEnv, builtPath)
	return builtPath
}

type buildError struct {
	err    error
	output string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.output }

// moduleRoot walks up from the test's working directory to the module
// root (the directory holding go.mod).
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
