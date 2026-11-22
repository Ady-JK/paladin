package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	view := LlmMessages([]transcript.Message{a.records[0], a.records[1]}, true, true)
	if len(view) != 2 || view[1].Image == nil || view[1].Image.SHA256 != ref.SHA256 {
		t.Fatalf("replayed image: %+v", view)
	}
}

// A session that sent images under a vision model (deepseek-v4.1-flash)
// must keep working after the user switches to a text-only model
// (glm-5.3): the replayed history never carries image parts the new
// endpoint would reject, and every image still reads as part of the
// exchange through a short note.
func TestTextOnlyModelGetsNotesInsteadOfReplayedImages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen.png")
	raw, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII=")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := media.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	image := &media.Image{Path: path, MIMEType: "image/png", Size: int64(len(raw)), SHA256: "hash"}
	attachment := media.Attachment{ID: "file-1", Name: "screen.png", Path: path, Image: image}
	user := transcript.UserMessage("look at this")
	user.Attachments = []media.Attachment{attachment}
	call := &inference.ToolCall{ID: "c1", Name: "read", Arguments: `{}`}
	name := "read"
	history := []transcript.Message{
		user,
		{Role: transcript.RoleAssistant, CreatedAt: time.Now().UTC(), ToolCalls: &[]transcript.ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}}},
		{Role: transcript.RoleTool, CreatedAt: time.Now().UTC(), ToolCallID: strptr("c1"), Name: &name, Content: "result", Image: ref},
		transcript.UserMessage("next"),
	}

	// A vision model sees the images; a text-only model sees notes.
	vision := LlmMessages(history, true, true)
	sawImage := 0
	for _, m := range vision {
		if m.Image != nil {
			sawImage++
		}
	}
	if sawImage != 2 {
		t.Fatalf("vision replay: %d images, want 2", sawImage)
	}
	blind := LlmMessages(history, true, false)
	for i, m := range blind {
		if m.Image != nil {
			t.Fatalf("text-only replay carried an image part at %d: %+v", i, m)
		}
		if strings.Contains(m.Content, "does not accept images") && m.Role != "tool" && m.Role != "user" {
			t.Fatalf("note landed on the wrong role: %+v", m)
		}
	}
	notes := 0
	for _, m := range blind {
		if strings.Contains(m.Content, "does not accept images") {
			notes++
		}
	}
	if notes != 2 {
		t.Fatalf("text-only replay notes: %d, want 2 (attachment + tool result)", notes)
	}

	// The current turn's fresh attachment gets the same treatment.
	a, _, _, done := testAgent(t, &mockProvider{})
	defer close(done)
	a.provider = &blindProvider{}
	a.currentAttachments = []media.Attachment{attachment}
	current := a.modelMessages("inspect it", false)
	for _, m := range current {
		if m.Image != nil {
			t.Fatalf("current attachment reached a text-only model: %+v", m)
		}
	}
	if len(current) != 2 || !strings.Contains(current[1].Content, "does not accept images") {
		t.Fatalf("current attachment note: %+v", current)
	}

	// A provider that says nothing about images keeps them, the
	// permissive default that never broke a vision model.
	a2, _, _, done2 := testAgent(t, &mockProvider{})
	defer close(done2)
	a2.currentAttachments = []media.Attachment{attachment}
	permissive := a2.modelMessages("inspect it", false)
	if len(permissive) != 2 || permissive[1].Image == nil {
		t.Fatalf("permissive default lost the image: %+v", permissive)
	}
	_ = call
}

// blindProvider is a provider whose listing said text-only.
type blindProvider struct{ mockProvider }

func (*blindProvider) AcceptsImages() bool { return false }
