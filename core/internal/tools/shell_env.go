// shell_env.go builds the child-process environment.
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
	"os/exec"
	"path/filepath"
	"sort"
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
	// nvm installs each node under a versioned dir that only lands on
	// PATH via ~/.bashrc (skipped by non-interactive shells), so probe
	// for installed versions directly, newest first.
	candidates = append(candidates, nvmNodeBins(home)...)
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

// nvmNodeBins returns ~/.nvm/versions/node/*/bin, newest version
// first, so `node` resolves the way the user's terminal sees it.
func nvmNodeBins(home string) []string {
	entries, err := os.ReadDir(filepath.Join(home, ".nvm", "versions", "node"))
	if err != nil {
		return nil
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "v") {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	var out []string
	for _, v := range versions {
		out = append(out, filepath.Join(home, ".nvm", "versions", "node", v, "bin"))
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
	rest = append(rest, "PATH="+augmentedPath(path))
	return append(rest, extra...)
}

// ChildEnv is shellEnv for other packages that launch user tools, such
// as MCP servers started with npx or uvx.
func ChildEnv(extra ...string) []string { return shellEnv(extra...) }

// LookCommand is lookPath for other packages that launch user tools.
func LookCommand(name string) (string, error) { return lookPath(name) }

// augmentedPath prepends the existing toolchain dirs to path,
// de-duplicated. Used for spawned commands and for looking up helper
// binaries (rg) when the sidecar itself runs under a minimal desktop
// environment.
func augmentedPath(path string) string {
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
			return strings.Join(prepend, sep) + sep + path
		}
		return strings.Join(prepend, sep)
	}
	return path
}

// lookPath finds a helper binary the way a spawned command would: the
// process PATH first, then the toolchain dirs (a desktop-spawned
// sidecar has a minimal PATH that may hide rg even though the user's
// shell would find it).
func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, d := range toolchainDirs() {
		p := filepath.Join(d, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// userShell resolves the shell model commands run under, mirroring
// opencode: $SHELL when it names a compatible executable, else
// /bin/bash, else the first usable entry in /etc/shells, else /bin/sh.
func userShell() string {
	if sh := strings.TrimSpace(os.Getenv("SHELL")); sh != "" && compatibleShell(sh) && isExecutable(sh) {
		return sh
	}
	if isExecutable("/bin/bash") {
		return "/bin/bash"
	}
	if data, err := os.ReadFile("/etc/shells"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if compatibleShell(line) && isExecutable(line) {
				return line
			}
		}
	}
	for _, fb := range []string{"/bin/bash", "/bin/sh"} {
		if isExecutable(fb) {
			return fb
		}
	}
	return "sh"
}

// compatibleShell rejects shells the model cannot target with
// sh-compatible syntax (opencode denies these for tool commands too).
func compatibleShell(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case "fish", "nu", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return false
	}
	return true
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0
}

// loginArgs builds the argv for a model command. It reports whether
// the shell runs as a login shell: bash/dash/ksh/sh/zsh (like
// opencode's login list) get -l so profile files assemble the user's
// real settings even when the sidecar was spawned with a minimal
// desktop environment.
func loginArgs(shell, cmdStr string) ([]string, bool) {
	switch strings.ToLower(filepath.Base(shell)) {
	case "bash", "rbash", "dash", "ksh", "sh", "zsh":
		return []string{"-l", "-c", cmdStr}, true
	default:
		return []string{"-c", cmdStr}, false
	}
}

// loginPathExport re-applies the toolchain dirs inside a login shell:
// /etc/profile resets PATH, wiping the augmented process environment.
// Appended (not prepended) so the user's own profile ordering wins;
// the dirs only fill gaps.
func loginPathExport() string {
	dirs := toolchainDirs()
	if len(dirs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("export PATH=\"$PATH")
	for _, d := range dirs {
		b.WriteString(":")
		b.WriteString(doubleQuoteEscape(d))
	}
	b.WriteString("\"; ")
	return b.String()
}

// doubleQuoteEscape escapes a path for use inside a double-quoted
// shell string. PATH entries split on ':' only, so spaces need no
// quoting — just backslash, '"', '$' and backtick.
func doubleQuoteEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\\' || r == '"' || r == '$' || r == '`' {
			b.WriteString("\\")
		}
		b.WriteRune(r)
	}
	return b.String()
}

// spawnShellCmd builds the process for a model command: the user's
// login shell with the augmented environment. Call sites add their
// own pipes, log files and process-group attributes.
func spawnShellCmd(cmdStr, dir string, extra ...string) *exec.Cmd {
	shell := userShell()
	args, login := loginArgs(shell, cmdStr)
	if login {
		args[2] = loginPathExport() + cmdStr
	}
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = shellEnv(extra...)
	return cmd
}
