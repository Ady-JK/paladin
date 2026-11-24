package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A read error that is not "missing" must not replace the user's edited
// prompt with the default.
func TestLoadPromptFileKeepsAnUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not block reads here")
	}
	path := filepath.Join(t.TempDir(), "system-prompt.md")
	if err := os.WriteFile(path, []byte("my custom prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if got := loadPromptFile(path, "default prompt"); got != "default prompt" {
		t.Fatalf("an unreadable file should fall back to the default for this turn, got %q", got)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "my custom prompt" {
		t.Fatalf("the user's prompt was overwritten: %q", b)
	}
}

func TestLoadPromptFileSeedsMissingAndBlankFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "a.md")
	if got := loadPromptFile(missing, "seed"); got != "seed" {
		t.Fatalf("got %q", got)
	}
	if b, _ := os.ReadFile(missing); string(b) != "seed" {
		t.Fatalf("missing file not seeded: %q", b)
	}
	blank := filepath.Join(dir, "b.md")
	if err := os.WriteFile(blank, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPromptFile(blank, "seed"); got != "seed" {
		t.Fatalf("got %q", got)
	}
	if b, _ := os.ReadFile(blank); string(b) != "seed" {
		t.Fatalf("blank file not reseeded: %q", b)
	}
}
