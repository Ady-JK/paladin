package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsNonAbsolute(t *testing.T) {
	if _, _, err := ResolveScopedPath("read", "path", "workspace/../etc/hosts", "/tmp/ws"); err == nil {
		t.Fatal("expected workspace/ prefix rejection")
	}
	if _, _, err := ResolveScopedPath("read", "path", "workspace/src/main.rs", "/tmp/ws"); err == nil {
		t.Fatal("expected workspace/ prefix rejection")
	}
	if _, _, err := ResolveScopedPath("read", "path", "src/main.rs", "/tmp/ws"); err == nil {
		t.Fatal("expected bare relative rejection")
	}
	// Absolute inside the workspace flags inside.
	resolved, scope, err := ResolveScopedPath("read", "path", "/tmp/ws/src/main.rs", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeInside || resolved != "/tmp/ws/src/main.rs" {
		t.Fatalf("got %q %q", resolved, scope)
	}
	// Absolute outside flags outside and stays allowed.
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
	// Absolute with no workspace flags outside and stays allowed.
	if _, _, err := ResolveScopedPath("read", "path", "/a/b", ""); err != nil {
		t.Fatal(err)
	}
	// Absolute inside the workspace flags inside.
	p, scope, err := ResolveScopedPath("read", "path", filepath.Join(ws, "a"), ws)
	if err != nil || scope != ScopeInside || p != filepath.Join(ws, "a") {
		t.Fatalf("got %q %q %v", p, scope, err)
	}
	// Bare relative rejected, absolute outside accepted.
	if _, _, err := ResolveScopedPath("read", "path", "a/b", ws); err == nil {
		t.Fatal("bare relative accepted")
	}
	p, scope, err = ResolveScopedPath("read", "path", "/etc/hosts", ws)
	if err != nil || scope != ScopeOutside || p != "/etc/hosts" {
		t.Fatalf("got %q %q %v", p, scope, err)
	}
	// Symlink escape flags outside instead of rejecting: a link inside
	// pointing outside is still reported.
	outside := t.TempDir()
	link := filepath.Join(ws, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	p, scope, err = ResolveScopedPath("read", "path", filepath.Join(ws, "link", "secret"), ws)
	if err != nil || scope != ScopeOutside {
		t.Fatalf("got %q %q %v", p, scope, err)
	}
	// cleanPath lexically resolves dot segments.
	if got := cleanPath("/a/./b/../c"); got != "/a/c" {
		t.Fatalf("got %q", got)
	}
}
