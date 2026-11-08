package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIDEReadSaveAndConflicts(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(file, []byte("old\n"), 0755); err != nil {
		t.Fatal(err)
	}
	view, err := IDEReadFile(dir, file)
	if err != nil || view.Content != "old\n" {
		t.Fatalf("read: %+v %v", view, err)
	}
	saved, err := IDESaveFile(dir, file, "new\n", view.Version)
	if err != nil || saved.Version == view.Version {
		t.Fatalf("save: %+v %v", saved, err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0755 {
		t.Fatalf("lost executable permissions: %v", info.Mode())
	}
	if err := os.WriteFile(file, []byte("external\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := IDESaveFile(dir, file, "my changes\n", saved.Version); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected conflict, got %v", err)
	}
	contents, _ := os.ReadFile(file)
	if string(contents) != "external\n" {
		t.Fatalf("overwrote external changes: %q", contents)
	}
}

func TestIDERejectsOutsideBinaryAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := IDEReadFile(dir, outside); err == nil {
		t.Fatal("outside file opened")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := IDEReadFile(dir, link); err == nil {
		t.Fatal("outside symlink opened")
	}
	binary := filepath.Join(dir, "binary")
	if err := os.WriteFile(binary, []byte{'a', 0, 'b'}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := IDEReadFile(dir, binary); err == nil {
		t.Fatal("binary file opened")
	}
}

func TestIDERejectsOversizedFilesAndInvalidText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(path, make([]byte, ideMaxFileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := IDEReadFile(dir, path); err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Fatalf("expected size error: %v", err)
	}
	if err := os.WriteFile(path, []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := IDEReadFile(dir, path); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}
