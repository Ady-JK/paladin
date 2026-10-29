// Package sandbox confines model shell commands at the OS level: writes
// are limited to the workspace (plus temp and dev caches) while reads
// and the network stay open. macOS uses Seatbelt (sandbox-exec), Linux
// uses Landlock through a re-exec helper, other systems have none and
// rely on approvals alone.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// HelperArg is argv[1] of the Linux Landlock re-exec: agent-host
// restricts itself and then execs the real command (see RunHelper).
const HelperArg = "__paladin_sandbox"

// HelperEnv overrides the executable that applies the confinement.
// Production leaves it empty and re-execs the running binary
// (agent-host). Tests must set it to a compiled agent-host: a test
// binary has no helper entry point, so re-execing it would re-run the
// whole suite inside itself, recursively.
const HelperEnv = "PALADIN_SANDBOX_HELPER"

// Mode is how much a sandboxed command may write.
type Mode string

const (
	// ModeNone runs the command unconfined.
	ModeNone Mode = ""
	// ModeReadOnly allows writes only to temp dirs and /dev/null.
	ModeReadOnly Mode = "read-only"
	// ModeWorkspaceWrite also allows the writable roots and dev caches.
	ModeWorkspaceWrite Mode = "workspace-write"
)

// Policy is one command's confinement.
type Policy struct {
	Mode Mode
	// Writable roots, usually just the workspace (workspace-write only).
	Writable []string
	// NoTemp drops the default temp and cache roots (tests).
	NoTemp bool
}

// ErrUnavailable means this system has no OS sandbox.
var ErrUnavailable = errors.New("no OS sandbox is available on this system")

// Kind names the active OS sandbox ("seatbelt", "landlock") or "" when
// commands can only be gated by approvals.
func Kind() string { return kind() }

// Wrap returns argv rewritten to run inside the policy.
func Wrap(p Policy, argv []string) ([]string, error) {
	switch p.Mode {
	case ModeNone:
		return argv, nil
	case ModeReadOnly, ModeWorkspaceWrite:
	default:
		return nil, fmt.Errorf("sandbox: unknown mode %q", p.Mode)
	}
	if len(argv) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	if Kind() == "" {
		return nil, ErrUnavailable
	}
	return wrap(writableRoots(p), argv)
}

// blockedMarkers are the errors a write denied by the sandbox surfaces as.
var blockedMarkers = []string{
	"operation not permitted",
	"read-only file system",
	"permission denied",
	"sandbox",
}

// LooksBlocked reports whether a failed command's output suggests the
// sandbox stopped it, so the caller can offer to rerun it unconfined.
func LooksBlocked(output string) bool {
	low := strings.ToLower(output)
	if strings.Contains(low, "no such file or directory") && !strings.Contains(low, "operation not permitted") {
		return false
	}
	for _, m := range blockedMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// writableRoots resolves every root the policy may write, both as given
// and symlink-resolved (macOS /var -> /private/var), deduplicated.
func writableRoots(p Policy) []string {
	var raw []string
	if p.Mode == ModeWorkspaceWrite {
		raw = append(raw, p.Writable...)
	}
	if !p.NoTemp {
		raw = append(raw, os.TempDir(), "/tmp", "/var/tmp")
		if p.Mode == ModeWorkspaceWrite {
			raw = append(raw, devCaches()...)
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = filepath.Clean(s)
		if s == "" || s == "/" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, r := range raw {
		if strings.TrimSpace(r) == "" || !filepath.IsAbs(r) {
			continue
		}
		add(r)
		if real, err := filepath.EvalSymlinks(r); err == nil {
			add(real)
		}
	}
	return out
}

// devCaches are per-user caches package managers and build tools write
// to; blocking them would turn every build into an escalation prompt.
func devCaches() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	rel := []string{".cache", ".npm", ".pnpm-store", ".local/share/pnpm", ".yarn", "go/pkg/mod",
		".cargo/registry", ".gradle", ".m2", ".rustup", ".bun/install/cache"}
	if runtime.GOOS == "darwin" {
		rel = append(rel, "Library/Caches", "Library/pnpm")
	}
	out := make([]string, 0, len(rel)+2)
	for _, r := range rel {
		out = append(out, filepath.Join(home, r))
	}
	for _, env := range []string{"GOMODCACHE", "GOCACHE"} {
		if v := os.Getenv(env); v != "" {
			out = append(out, v)
		}
	}
	return out
}
