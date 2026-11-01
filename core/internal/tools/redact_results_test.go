package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/redact"
)

// The user's stored keys never reach the model, whichever tool would
// carry them: read, grep and every other result mask them. Patterns
// that merely look like keys stay in file contents, so edits still match.
func TestStoredKeysAreMaskedInEveryToolResult(t *testing.T) {
	redact.Reset()
	t.Cleanup(redact.Reset)
	key := "sk-live-PaladinStoredKey-1234567890"
	redact.Register(key)
	ctx, ws := testCtx(t)
	path := filepath.Join(ws, "config.txt")
	os.WriteFile(path, []byte("provider="+key+"\nexample=sk-example-not-stored-0000000000\n"), 0o644)
	r := BuiltinRegistry()
	for _, c := range []ToolCall{
		{ID: "r", Name: "read", Arguments: argsJSON(map[string]any{"path": path})},
		{ID: "g", Name: "grep", Arguments: argsJSON(map[string]any{"pattern": "provider", "path": ws})},
	} {
		res := r.Call(&c, ctx)
		if res.IsError || strings.Contains(res.Content, key) || !strings.Contains(res.Content, "[redacted]") {
			t.Fatalf("%s leaked the stored key: %s", c.Name, res.Content)
		}
	}
	res := r.Call(&ToolCall{ID: "r2", Name: "read", Arguments: argsJSON(map[string]any{"path": path})}, ctx)
	if !strings.Contains(res.Content, "sk-example-not-stored-0000000000") {
		t.Fatalf("a look-alike in a file was masked: %s", res.Content)
	}
}

func argsJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
