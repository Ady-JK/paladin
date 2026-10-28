package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserShellRespectsShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	if got := userShell(); got != "/bin/sh" {
		t.Fatalf("got %q", got)
	}
}

func TestUserShellSkipsIncompatible(t *testing.T) {
	t.Setenv("SHELL", "/bin/fish")
	if got := userShell(); got == "/bin/fish" {
		t.Fatal("fish must not be selected for sh-compatible commands")
	}
}

func TestUserShellSkipsMissing(t *testing.T) {
	t.Setenv("SHELL", "/nonexistent/shell")
	got := userShell()
	if got == "/nonexistent/shell" {
		t.Fatal("missing shell selected")
	}
	if got == "" {
		t.Fatal("empty shell")
	}
}

func TestCompatibleShell(t *testing.T) {
	for _, bad := range []string{"/bin/fish", "nu", "/usr/bin/pwsh", "cmd"} {
		if compatibleShell(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"/bin/bash", "/bin/sh", "/bin/zsh", "/bin/dash"} {
		if !compatibleShell(ok) {
			t.Fatalf("%q rejected", ok)
		}
	}
}

func TestLoginArgs(t *testing.T) {
	args, login := loginArgs("/bin/bash", "ls")
	if !login || len(args) != 3 || args[0] != "-l" || args[1] != "-c" {
		t.Fatalf("bash should run login: %q", args)
	}
	args, login = loginArgs("/bin/sh", "ls")
	if !login || len(args) != 3 || args[0] != "-l" {
		t.Fatalf("sh should run login: %q", args)
	}
	args, login = loginArgs("/bin/fish", "ls")
	if login || len(args) != 2 || args[0] != "-c" {
		t.Fatalf("fish has no login flag here: %q", args)
	}
}

func TestLoginPathExportSurvivesProfileReset(t *testing.T) {
	// /etc/profile resets PATH on login, wiping the process env. The
	// injected export must restore the toolchain dirs afterwards.
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("no bash")
	}
	t.Setenv("SHELL", "/bin/bash")
	cmd := spawnShellCmd(`printf %s "$PATH"`, t.TempDir())
	// Simulate the Electron-spawned sidecar: minimal PATH, injected
	// after profiles reset it.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "PATH=/usr/bin:/bin")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("login shell failed: %v", err)
	}
	for _, want := range toolchainDirs() {
		if !strings.Contains(string(out), want) {
			t.Fatalf("toolchain dir %q missing from login PATH %q", want, out)
		}
	}
}

// TestLoginShellSourcesProfile proves the point of the login shell:
// profile files assemble the user's settings even when the parent
// environment is minimal (the Electron-spawned sidecar case).
func TestLoginShellSourcesProfile(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("no bash")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("export PALADIN_LOGIN_TEST_MARKER=from-profile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	cmd := spawnShellCmd(`printf %s "$PALADIN_LOGIN_TEST_MARKER"`, t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("login shell failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "from-profile" {
		t.Fatalf("profile not sourced, got %q", out)
	}
}

func TestLookPathFindsHelper(t *testing.T) {
	if _, err := lookPath("sh"); err != nil {
		t.Fatalf("sh not found: %v", err)
	}
	if _, err := lookPath("definitely-not-a-binary-xyz"); err == nil {
		t.Fatal("missing binary found")
	}
}
