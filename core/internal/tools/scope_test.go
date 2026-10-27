package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectsEscapeAndBareRelative(t *testing.T) {
	if _, _, err := ResolveScopedPath("read", "path", "workspace/../etc/hosts", "/tmp/ws"); err == nil {
		t.Fatal("expected escape rejection")
	}
	if _, _, err := ResolveScopedPath("read", "path", "src/main.rs", "/tmp/ws"); err == nil {
		t.Fatal("expected bare relative rejection")
	}
	resolved, scope, err := ResolveScopedPath("read", "path", "workspace/src/main.rs", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeInside || resolved != "/tmp/ws/src/main.rs" {
		t.Fatalf("got %q %q", resolved, scope)
	}
	resolved, scope, err = ResolveScopedPath("read", "path", "/etc/hosts", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeOutside || resolved != "/etc/hosts" {
		t.Fatalf("got %q %q", resolved, scope)
	}
}

func TestScopeMatrix(t *testing.T) {
	ws := t.TempDir()
	// Empty path.
	if _, _, err := ResolveScopedPath("read", "path", "   ", ws); err == nil {
		t.Fatal("empty accepted")
	}
	// No workspace.
	if _, _, err := ResolveScopedPath("read", "path", "workspace/a", ""); err == nil {
		t.Fatal("missing workspace accepted")
	}
	// Escape via ..
	if _, _, err := ResolveScopedPath("read", "path", "workspace/../etc/hosts", ws); err == nil {
		t.Fatal("escape accepted")
	}
	// Bare relative rejected, absolute accepted.
	if _, _, err := ResolveScopedPath("read", "path", "a/b", ws); err == nil {
		t.Fatal("bare relative accepted")
	}
	p, scope, err := ResolveScopedPath("read", "path", "/etc/hosts", ws)
	if err != nil || scope != ScopeOutside || p != "/etc/hosts" {
		t.Fatalf("got %q %q %v", p, scope, err)
	}
	// workspace/ prefix alone resolves to the root.
	p, scope, err = ResolveScopedPath("read", "path", "workspace", ws)
	if err != nil || scope != ScopeInside || p != ws {
		t.Fatalf("got %q %q %v", p, scope, err)
	}
	// Backslash prefix works.
	p, _, err = ResolveScopedPath("read", "path", `workspace\sub\a.txt`, ws)
	if err != nil || !strings.HasSuffix(p, filepath.Join("sub", "a.txt")) {
		t.Fatalf("got %q %v", p, err)
	}
	// Symlink escape is rejected: link inside pointing outside.
	outside := t.TempDir()
	link := filepath.Join(ws, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveScopedPath("read", "path", "workspace/link/secret", ws); err == nil {
		t.Fatal("symlink escape accepted")
	}
	// cleanPath lexically resolves dot segments.
	if got := cleanPath("/a/./b/../c"); got != "/a/c" {
		t.Fatalf("got %q", got)
	}
}
