package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFindBinaryRejectsAnUnavailableOverride(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "not-executable"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex")
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "not-executable":
				if err := os.WriteFile(path, []byte("not a CLI"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(BinEnv, path)
			if _, err := FindBinary(); err == nil {
				t.Fatal("an unavailable override was reported as installed")
			}
		})
	}
}

// isolated gives FindBinary an empty PATH, an empty home and one fake
// Applications folder, so the machine's own Codex cannot answer.
func isolated(t *testing.T) (apps string) {
	t.Helper()
	t.Setenv(BinEnv, "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	apps = t.TempDir()
	old, oldShell := applicationDirs, shellPATH
	applicationDirs = func(string) []string { return []string{apps} }
	shellPATH = func() string { return "" }
	t.Cleanup(func() { applicationDirs, shellPATH = old, oldShell })
	return apps
}

// The Codex desktop app is now ChatGPT.app, which carries the CLI inside it and
// puts nothing on PATH. A machine with only the app must still count as having Codex.
func TestFindBinaryUsesTheCLIBundledInTheDesktopApp(t *testing.T) {
	apps := isolated(t)
	want := filepath.Join(apps, "ChatGPT.app", "Contents", "Resources", "codex-cli", "bin", "codex")
	executable(t, want)
	got, err := FindBinary()
	if err != nil || got != want {
		t.Fatalf("FindBinary = %q, %v; want %q", got, err, want)
	}
}

func TestFindBinaryUsesTheOlderAppLayoutToo(t *testing.T) {
	apps := isolated(t)
	want := filepath.Join(apps, "Codex.app", "Contents", "Resources", "codex")
	executable(t, want)
	if got, err := FindBinary(); err != nil || got != want {
		t.Fatalf("FindBinary = %q, %v; want %q", got, err, want)
	}
}

func TestFindBinaryPrefersPATHOverAnAppBundle(t *testing.T) {
	apps := isolated(t)
	executable(t, filepath.Join(apps, "ChatGPT.app", "Contents", "Resources", "codex-cli", "bin", "codex"))
	onPath := t.TempDir()
	executable(t, filepath.Join(onPath, "codex"))
	t.Setenv("PATH", onPath)
	if got, err := FindBinary(); err != nil || got != filepath.Join(onPath, "codex") {
		t.Fatalf("FindBinary = %q, %v", got, err)
	}
}

func TestFindBinaryIgnoresADirectoryNamedCodex(t *testing.T) {
	apps := isolated(t)
	if err := os.MkdirAll(filepath.Join(apps, "ChatGPT.app", "Contents", "Resources", "codex-cli", "bin", "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := FindBinary(); err == nil {
		t.Fatal("a directory was taken for the executable")
	}
}

// A Dock-launched app inherits almost no PATH. The CLI installed with pnpm is a
// shell shim that needs `node`, and both live on the PATH the user's own shell
// builds (.zshrc: pnpm, nvm). Paladin asks that shell, once, and uses its PATH
// both to find codex and to run it.
func TestFindBinaryAsksTheUsersShell(t *testing.T) {
	isolated(t)
	shimDir := t.TempDir()
	executable(t, filepath.Join(shimDir, "codex"))
	old := shellPATH
	shellPATH = func() string { return "/nowhere:" + shimDir }
	t.Cleanup(func() { shellPATH = old })
	if got, err := FindBinary(); err != nil || got != filepath.Join(shimDir, "codex") {
		t.Fatalf("FindBinary = %q, %v", got, err)
	}
}

func TestFindBinaryPrefersYourOwnCLIOverTheAppBundle(t *testing.T) {
	apps := isolated(t)
	executable(t, filepath.Join(apps, "ChatGPT.app", "Contents", "Resources", "codex-cli", "bin", "codex"))
	shimDir := t.TempDir()
	executable(t, filepath.Join(shimDir, "codex"))
	old := shellPATH
	shellPATH = func() string { return shimDir }
	t.Cleanup(func() { shellPATH = old })
	if got, _ := FindBinary(); got != filepath.Join(shimDir, "codex") {
		t.Fatalf("FindBinary = %q, want the CLI on the shell's PATH", got)
	}
}

func TestChildEnvGivesCodexTheShellsPATH(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	old := shellPATH
	shellPATH = func() string { return "/home/me/.nvm/bin:/usr/bin:/home/me/pnpm" }
	t.Cleanup(func() { shellPATH = old })
	var path string
	for _, kv := range childEnv() {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	if path != "/home/me/.nvm/bin:/usr/bin:/home/me/pnpm:/bin" {
		t.Fatalf("PATH = %q: shell entries first, each once, then what the app had", path)
	}
}

func TestParseShellPATHIgnoresWhatTheRCFilesPrint(t *testing.T) {
	out := "Welcome back!\nsome banner\n__PALADIN_PATH__/a/bin:/b/bin__PALADIN_END__\ntrailing noise\n"
	if got := parseShellPATH(out); got != "/a/bin:/b/bin" {
		t.Fatalf("got %q", got)
	}
	if parseShellPATH("no markers here") != "" {
		t.Fatal("took noise for a PATH")
	}
}
