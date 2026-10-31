package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
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
	res := r.Call(&ToolCall{ID: "read-1", Name: "read", Arguments: `{"path":"` + path + `"}`}, &ToolCtx{Workspace: root, Cancel: context.Background()})
	if res.IsError || res.Image == nil || res.Image.MIMEType != "image/png" {
		t.Fatalf("read image: %+v", res)
	}
	if len(res.Content) > 2048 {
		t.Fatalf("image metadata too large: %d", len(res.Content))
	}
}
