package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestToolAddAndView(t *testing.T) {
	bank := NewBank(t.TempDir())
	tool := NewTool(bank)
	ctx := &tools.ToolCtx{Workspace: "/repo"}
	out, err := tool.Call(json.RawMessage(`{"target":"project","operations":[{"action":"add","content":"run go test ./..."}]}`), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"saved":true`) || !strings.Contains(out, "run go test") {
		t.Fatalf("out = %s", out)
	}
	view, err := tool.Call(json.RawMessage(`{"target":"project"}`), ctx)
	if err != nil || !strings.Contains(view, "run go test") || strings.Contains(view, "saved") {
		t.Fatalf("view = %s err=%v", view, err)
	}
}

func TestToolErrors(t *testing.T) {
	tool := NewTool(NewBank(t.TempDir()))
	for _, raw := range []string{`{"target":"nope"}`, `{"target":"global","operations":[{"action":"zap"}]}`, `not json`} {
		if _, err := tool.Call(json.RawMessage(raw), &tools.ToolCtx{}); err == nil {
			t.Fatalf("want error for %s", raw)
		}
	}
	if _, err := tool.Call(json.RawMessage(`{"target":"project"}`), &tools.ToolCtx{}); err == nil {
		t.Fatal("project without workspace must fail")
	}
}

func TestToolOverflowReturnsEntries(t *testing.T) {
	tool := NewTool(NewBank(t.TempDir()))
	big := strings.Repeat("y", MaxEntry-5)
	ctx := &tools.ToolCtx{Workspace: "/r"}
	for i := 0; i < 5; i++ {
		raw, _ := json.Marshal(map[string]any{"target": "user", "operations": []map[string]string{{"action": "add", "content": big + string(rune('a'+i))}}})
		_, err := tool.Call(raw, ctx)
		if err != nil {
			te := err.(*tools.ToolError)
			if !strings.Contains(te.ResultContent(), "entries") {
				t.Fatalf("overflow should list entries: %s", te.ResultContent())
			}
			return
		}
	}
	t.Fatal("expected overflow")
}
