// Package tools: shell_env.go builds the child-process environment.
//
// The sidecar is usually spawned by the Electron app, which inherits a
// minimal desktop PATH (/usr/bin:/bin) without the toolchain dirs a
// login shell adds. That made plain `go`, `cargo` or `node` fail with
// "not found" even though they work in the user's terminal. Every
// shell spawn goes through shellEnv so the model can rely on the same
// commands the user has.
package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// toolchainDirs are prepended to PATH when missing. Lookups probe the
// real filesystem so we only add dirs that exist.
func toolchainDirs() []string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/usr/local/go/bin",
		"/opt/go/bin",
		"/usr/local/bin",
		"/opt/homebrew/bin",
		"/opt/local/bin",
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".opencode", "bin"),
		filepath.Join(home, ".bun", "bin"),
	}
	var out []string
	for _, d := range candidates {
		if d == "" || d == "/" {
			continue
		}
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, d)
	}
	return out
}

// shellEnv returns the environment for spawned commands: the parent
// environment plus the toolchain dirs above on PATH, plus the
// non-interactive sanitisers each spawn site already set.
func shellEnv(extra ...string) []string {
	env := os.Environ()
	var path string
	rest := make([]string, 0, len(env)+len(extra))
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
			continue
		}
		rest = append(rest, kv)
	}
	sep := string(os.PathListSeparator)
	have := map[string]bool{}
	for _, p := range strings.Split(path, sep) {
		if p != "" {
			have[p] = true
		}
	}
	var prepend []string
	for _, d := range toolchainDirs() {
		if !have[d] {
			prepend = append(prepend, d)
			have[d] = true
		}
	}
	if len(prepend) > 0 {
		if path != "" {
			path = strings.Join(prepend, sep) + sep + path
		} else {
			path = strings.Join(prepend, sep)
		}
	}
	out := append(rest, "PATH="+path)
	return append(out, extra...)
}
