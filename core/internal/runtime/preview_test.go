package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func previewFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A tagged text file yields its first non-empty lines, relative to the
// workspace, so the composer can show a preview card.
func TestFilePreviewText(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	path := previewFile(t, ws, "src/plan.md", "\n# Plan\n\nfirst line\nsecond line\nthird\nfourth\nfifth\n")
	view, err := rt.FilePreview(ws, path)
	if err != nil {
		t.Fatal(err)
	}
	if view.Kind != "text" || view.Rel != "src/plan.md" {
		t.Fatalf("preview = %+v", view)
	}
	if got := strings.Join(view.Lines, "|"); got != "# Plan|first line|second line|third" {
		t.Fatalf("lines = %q", got)
	}
}

// A small image comes back as a data URL for the card thumbnail.
func TestFilePreviewImage(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	path := previewFile(t, ws, "shot.png", "\x89PNG\r\n\x1a\n"+strings.Repeat("x", 32))
	view, err := rt.FilePreview(ws, path)
	if err != nil {
		t.Fatal(err)
	}
	if view.Kind != "image" || !strings.HasPrefix(view.DataURL, "data:image/png;base64,") {
		t.Fatalf("preview = %+v", view)
	}
}

// Binary, folders, outside paths and absent files still name their kind
// so a tag card always renders.
func TestFilePreviewKinds(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	cases := []struct {
		name string
		path string
		kind string
	}{
		{"binary", previewFile(t, ws, "bin.dat", "a\x00b"), "binary"},
		{"dir", ws, "dir"},
		{"missing", filepath.Join(ws, "gone.txt"), "missing"},
	}
	outside := t.TempDir()
	cases = append(cases, struct {
		name string
		path string
		kind string
	}{"outside", previewFile(t, outside, "other.txt", "hi"), "outside"})
	for _, tc := range cases {
		view, err := rt.FilePreview(ws, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if view.Kind != tc.kind {
			t.Fatalf("%s: kind = %q, want %q (%+v)", tc.name, view.Kind, tc.kind, view)
		}
		if tc.kind != "text" && view.DataURL != "" && tc.kind != "image" {
			t.Fatalf("%s: unexpected data URL", tc.name)
		}
	}
}

// A relative path is a caller bug, not a silent workspace guess.
func TestFilePreviewRejectsRelativePath(t *testing.T) {
	rt, _ := testRuntime(t)
	if _, err := rt.FilePreview(t.TempDir(), "src/main.go"); err == nil {
		t.Fatal("relative path accepted")
	}
}
