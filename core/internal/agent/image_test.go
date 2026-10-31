package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/tools"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestImageToolResultSurvivesTranscriptReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen.png")
	raw, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII=")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := media.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _, _, done := testAgent(t, &mockProvider{})
	defer close(done)
	call := &inference.ToolCall{ID: "c1", Name: "read", Arguments: `{}`}
	a.appendAssistant("", []inference.ToolCall{*call})
	a.appendToolResult(call, &tools.ToolResult{CallID: "c1", Name: "read", Content: "image", Image: ref})
	if a.messages[1].Image == nil || a.records[1].Image == nil {
		t.Fatal("image lost on append")
	}
	view := LlmMessages([]transcript.Message{a.records[0], a.records[1]}, true)
	if len(view) != 2 || view[1].Image == nil || view[1].Image.SHA256 != ref.SHA256 {
		t.Fatalf("replayed image: %+v", view)
	}
}
