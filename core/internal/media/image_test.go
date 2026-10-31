package media

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII="

func TestReadImageAndDetectChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "picture.bin")
	raw, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if ref.MIMEType != "image/png" || ref.Size != int64(len(raw)) {
		t.Fatalf("bad image ref: %+v", ref)
	}
	url, err := ref.DataURL()
	if err != nil || url != "data:image/png;base64,"+onePixelPNG {
		t.Fatalf("data URL: %q, %v", url, err)
	}
	if err := os.WriteFile(path, append(raw, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ref.DataURL(); err == nil {
		t.Fatal("changed image should be rejected")
	}
}

func TestReadRejectsUnsupportedAndOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "picture.svg")
	if err := os.WriteFile(path, []byte("<svg></svg>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("SVG must not become model image")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("oversize image must be rejected")
	}
}
