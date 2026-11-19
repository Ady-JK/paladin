package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// A GUI app started from the Dock or Finder inherits almost no PATH, while the
// user's Codex is usually installed through something their shell sets up: pnpm,
// nvm, Volta, Homebrew. The pnpm install is a shell shim that runs `node`, so
// finding it is not enough, it must also run with the PATH that has node on it.
// Paladin therefore asks the user's own shell for its PATH, once, the way editors
// resolve a shell environment.

const (
	pathStart = "__PALADIN_PATH__"
	pathEnd   = "__PALADIN_END__"
	// shellTimeout bounds how long a slow rc file can hold up looking for Codex.
	shellTimeout = 5 * time.Second
)

// shellPATH is the PATH the user's login shell builds ("" when it cannot be
// read). A variable so tests can stand in for the shell.
var shellPATH = loginShellPATH

var (
	shellOnce sync.Once
	shellPath string
)

// loginShellPATH runs the user's shell once, as an interactive login shell so it
// reads the files where PATH is usually extended (.zprofile and .zshrc), and
// reports its PATH. Interactive rc files may print; only what sits between the
// markers is read.
func loginShellPATH() string {
	shellOnce.Do(func() {
		if runtime.GOOS == "windows" {
			return
		}
		shell := strings.TrimSpace(os.Getenv("SHELL"))
		if shell == "" {
			shell = "/bin/zsh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, shell, "-ilc", `printf '%s%s%s' '`+pathStart+`' "$PATH" '`+pathEnd+`'`)
		cmd.Stdin = nil // /dev/null: an rc file must not wait for a terminal
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			return
		}
		shellPath = parseShellPATH(string(out))
	})
	return shellPath
}

// parseShellPATH picks the PATH out from between the markers.
func parseShellPATH(out string) string {
	_, rest, ok := strings.Cut(out, pathStart)
	if !ok {
		return ""
	}
	path, _, ok := strings.Cut(rest, pathEnd)
	if !ok {
		return ""
	}
	return strings.TrimSpace(path)
}

// lookPathIn finds an executable file by name in a PATH-style list.
func lookPathIn(name, path string) (string, bool) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate, true
		}
	}
	return "", false
}

// childEnv is the environment Codex runs with: the app's own, with PATH led by the
// user's shell PATH (each directory once), so a shim finds the `node` it needs.
func childEnv() []string {
	env := os.Environ()
	shell := shellPATH()
	if shell == "" {
		return env
	}
	seen := map[string]bool{}
	var dirs []string
	for _, list := range []string{shell, os.Getenv("PATH")} {
		for _, dir := range filepath.SplitList(list) {
			if dir != "" && !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	merged := "PATH=" + strings.Join(dirs, string(os.PathListSeparator))
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = merged
			return env
		}
	}
	return append(env, merged)
}
