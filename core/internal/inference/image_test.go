package inference

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/media"
)

func imageMessages(t *testing.T) []LlmMessage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen.png")
	raw, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII=")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := media.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	id := "tool-1"
	return []LlmMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Name: "read", Arguments: `{}`}}},
		{Role: "tool", Content: `{"content":"[image attached]"}`, ToolCallID: &id, Name: strPtr("read"), Image: ref},
	}
}

func TestImageReachesEveryProviderDialect(t *testing.T) {
	messages := imageMessages(t)
	cases := map[string]any{
		"chat":      toSDKMessages(messages),
		"responses": toResponsesInput(messages),
		"anthropic": toAnthropicRequest("", messages, nil),
		"google":    toGoogleRequest("", messages, nil),
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "data:image/png;base64,") && !strings.Contains(string(raw), "iVBORw0KGgo") {
				t.Fatalf("missing image payload: %s", raw)
			}
		})
	}
}
