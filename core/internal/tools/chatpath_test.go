package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveChatPathInside(t *testing.T) {
	ws := t.TempDir()
	got, scope, err := ResolveChatPath("src/foo.ts", ws)
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeInside || got != filepath.Join(ws, "src", "foo.ts") {
		t.Fatalf("got %q %q", got, scope)
	}
}

func TestResolveChatPathDotDot(t *testing.T) {
	ws := t.TempDir()
	// ../ escapes the workspace: allowed here, reported outside.
	got, scope, err := ResolveChatPath("../sibling/file.txt", ws)
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeOutside {
		t.Fatalf("got %q %q", got, scope)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "sibling/file.txt") {
		t.Fatalf("got %q", got)
	}
	parent := filepath.Dir(ws)
	want := filepath.Join(parent, "sibling", "file.txt")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// workspace/-prefixed escape degrades to outside instead of failing.
	got, scope, err = ResolveChatPath("workspace/../etc/hosts", ws)
	if err != nil {
		t.Fatal(err)
	}
	if scope != ScopeOutside || got != filepath.Join(parent, "etc", "hosts") {
		t.Fatalf("got %q %q", got, scope)
	}
}

func TestResolveChatPathAbsAndWorkspace(t *testing.T) {
	ws := t.TempDir()
	got, scope, err := ResolveChatPath("/etc/hosts", ws)
	if err != nil || scope != ScopeOutside || got != "/etc/hosts" {
		t.Fatalf("got %q %q %v", got, scope, err)
	}
	// Windows absolute with backslashes.
	got, scope, err = ResolveChatPath(`C:\proj\file.txt`, ws)
	if err != nil || scope != ScopeOutside {
		t.Fatalf("got %q %q %v", got, scope, err)
	}
	if filepath.ToSlash(got) != "C:/proj/file.txt" {
		t.Fatalf("got %q", got)
	}
	// Inside the workspace via absolute path.
	inside := filepath.Join(ws, "a.txt")
	got, scope, err = ResolveChatPath(filepath.ToSlash(inside), ws)
	if err != nil || scope != ScopeInside || got != inside {
		t.Fatalf("got %q %q %v", got, scope, err)
	}
	// workspace/ prefix.
	got, scope, err = ResolveChatPath("workspace/sub/a.txt", ws)
	if err != nil || scope != ScopeInside || got != filepath.Join(ws, "sub", "a.txt") {
		t.Fatalf("got %q %q %v", got, scope, err)
	}
}

func TestResolveChatPathErrors(t *testing.T) {
	if _, _, err := ResolveChatPath("   ", t.TempDir()); err == nil {
		t.Fatal("empty accepted")
	}
	if _, _, err := ResolveChatPath("a/b", ""); err == nil {
		t.Fatal("relative without workspace accepted")
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		got, scope, err := ResolveChatPath("~/notes.txt", t.TempDir())
		if err != nil || scope != ScopeOutside || got != filepath.Join(home, "notes.txt") {
			t.Fatalf("got %q %q %v", got, scope, err)
		}
	}
}

func TestToChatPathRoundTrip(t *testing.T) {
	ws := t.TempDir()
	inside := filepath.Join(ws, "src", "foo.ts")
	if got := ToChatPath(inside, ws); got != "src/foo.ts" {
		t.Fatalf("got %q", got)
	}
	outside := filepath.Join(filepath.Dir(ws), "sib", "f.txt")
	if got := ToChatPath(outside, ws); got != "../sib/f.txt" {
		t.Fatalf("got %q", got)
	}
	abs := ToChatPath("/etc/hosts", ws)
	if !strings.HasPrefix(abs, "../") && abs != "/etc/hosts" {
		t.Fatalf("got %q", abs)
	}
	// Absolute outside paths round-trip through the ../ chain.
	backAbs, backScope, err := ResolveChatPath(abs, ws)
	if err != nil || backScope != ScopeOutside || backAbs != "/etc/hosts" {
		t.Fatalf("got %q %q %v", backAbs, backScope, err)
	}
	// Resolve(ToChatPath) is the identity for inside files.
	back, scope, err := ResolveChatPath(ToChatPath(inside, ws), ws)
	if err != nil || scope != ScopeInside || back != inside {
		t.Fatalf("got %q %q %v", back, scope, err)
	}
	back, scope, err = ResolveChatPath(ToChatPath(outside, ws), ws)
	if err != nil || back != outside {
		t.Fatalf("got %q %q %v", back, scope, err)
	}
}
