package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadImageReturnsModelImage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "screen.png")
	raw, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII=")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.Register(ReadTool{})
	res := r.Call(&ToolCall{ID: "read-1", Name: "read", Arguments: `{"reason":"test","path":"` + path + `"}`}, &ToolCtx{Workspace: root, Cancel: context.Background()})
	if res.IsError || res.Image == nil || res.Image.MIMEType != "image/png" {
		t.Fatalf("read image: %+v", res)
	}
	if len(res.Content) > 2048 {
		t.Fatalf("image metadata too large: %d", len(res.Content))
	}
}

// A text-only model gets a note instead of the image: the call must not
// produce an image part the endpoint would reject, and the note says
// what happened so the model adapts instead of retrying.
func TestReadImageTextOnlyModelGetsNote(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "screen.png")
	raw, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII=")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	no := false
	r := NewRegistry()
	r.Register(ReadTool{})
	res := r.Call(&ToolCall{ID: "read-1", Name: "read", Arguments: `{"reason":"test","path":"` + path + `"}`},
		&ToolCtx{Workspace: root, Cancel: context.Background(), ModelImages: &no})
	if res.IsError {
		t.Fatalf("text-only read must not error: %+v", res)
	}
	if res.Image != nil {
		t.Fatal("text-only model must not receive an image part")
	}
	if !strings.Contains(res.Content, "screen.png") || !strings.Contains(res.Content, "does not accept image inputs") {
		t.Fatalf("refusal note missing: %s", res.Content)
	}
}

// An unknown capability stays permissive: a listing that said nothing
// must never blind a vision model.
func TestReadImageUnknownCapabilityStaysPermissive(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "screen.png")
	raw, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII=")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.Register(ReadTool{})
	res := r.Call(&ToolCall{ID: "read-1", Name: "read", Arguments: `{"reason":"test","path":"` + path + `"}`},
		&ToolCtx{Workspace: root, Cancel: context.Background()})
	if res.IsError || res.Image == nil {
		t.Fatalf("unknown capability must keep images: %+v", res)
	}
}
