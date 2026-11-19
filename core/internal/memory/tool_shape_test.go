package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// The reported failures: a target nested inside an operation, and
// operations sent as a JSON string. Both used to error out; a save is the
// whole point of the tool, so both must now work.
func TestToolAcceptsTargetInsideOperation(t *testing.T) {
	tool := NewTool(NewBank(t.TempDir()))
	out, err := tool.Call(json.RawMessage(
		`{"operations":[{"action":"add","content":"prefers diagrams","target":"user"}]}`),
		&tools.ToolCtx{Workspace: "/repo"})
	if err != nil {
		t.Fatalf("nested target must be accepted: %v", err)
	}
	if !strings.Contains(out, `"saved":true`) || !strings.Contains(out, "prefers diagrams") {
		t.Fatalf("out = %s", out)
	}
	if !strings.Contains(out, `"target":"user"`) {
		t.Fatalf("hoisted target missing: %s", out)
	}
}

func TestToolAcceptsOperationsAsJSONString(t *testing.T) {
	tool := NewTool(NewBank(t.TempDir()))
	ops := `[{\"action\":\"add\",\"content\":\"uses pnpm\"}]`
	raw := `{"target":"global","operations":"` + ops + `"}`
	out, err := tool.Call(json.RawMessage(raw), &tools.ToolCtx{})
	if err != nil {
		t.Fatalf("stringified operations must be accepted: %v", err)
	}
	if !strings.Contains(out, "uses pnpm") {
		t.Fatalf("out = %s", out)
	}
}

// A single operation object, alias field names, and a remove that names
// its entry in content all save instead of failing.
func TestToolToleratesShapeVariants(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"single object", `{"target":"user","operations":{"action":"add","content":"likes terse replies"}}`},
		{"alias keys", `{"scope":"user","ops":[{"op":"add","text":"prefers Go"}]}`},
		{"read mode", `{"mode":"user"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool := NewTool(NewBank(t.TempDir()))
			if _, err := tool.Call(json.RawMessage(c.raw), &tools.ToolCtx{}); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
		})
	}
}

// `remove` names its entry either way round: content is accepted when
// old_text was left out.
func TestToolRemovesByContent(t *testing.T) {
	tool := NewTool(NewBank(t.TempDir()))
	if _, err := tool.Call(json.RawMessage(`{"target":"user","operations":[{"action":"add","content":"prefers Go"}]}`), &tools.ToolCtx{}); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Call(json.RawMessage(`{"target":"user","operations":[{"action":"remove","content":"prefers Go"}]}`), &tools.ToolCtx{})
	if err != nil {
		t.Fatalf("remove by content: %v", err)
	}
	if strings.Contains(out, "prefers Go") {
		t.Fatalf("entry not removed: %s", out)
	}
}

// Two different targets in one call is a real ambiguity: refuse it rather
// than silently writing to one.
func TestToolRefusesConflictingTargets(t *testing.T) {
	tool := NewTool(NewBank(t.TempDir()))
	raw := `{"target":"user","operations":[{"action":"add","content":"x","target":"global"}]}`
	_, err := tool.Call(json.RawMessage(raw), &tools.ToolCtx{})
	if err == nil {
		t.Fatal("conflicting targets must be refused")
	}
	if !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("err = %v", err)
	}
}

// An unfixable argument error must show the expected shape so the model
// can retry correctly instead of repeating the same call.
func TestToolArgumentErrorShowsShape(t *testing.T) {
	tool := NewTool(NewBank(t.TempDir()))
	for _, raw := range []string{`{}`, `{"target":"nope"}`, `{"operations":[{"action":"zap"}]}`, `not json`} {
		_, err := tool.Call(json.RawMessage(raw), &tools.ToolCtx{})
		if err == nil {
			t.Fatalf("want error for %s", raw)
		}
		content := err.(*tools.ToolError).ResultContent()
		// The hint rides inside a JSON payload, so match the escaped form
		// of the shape rather than raw quotes.
		if !strings.Contains(content, `\"target\"`) || !strings.Contains(content, "top level") {
			t.Fatalf("shape hint missing for %s: %s", raw, content)
		}
	}
}

// The three modes are named, and each keeps its own store.
func TestThreeModesAreNamedAndSeparate(t *testing.T) {
	bank := NewBank(t.TempDir())
	tool := NewTool(bank)
	ctx := &tools.ToolCtx{Workspace: "/repo"}
	want := map[string]string{"user": "User Preferences", "global": "Global Rules", "project": "Workspace Rules"}
	for target, title := range want {
		out, err := tool.Call(json.RawMessage(`{"target":"`+target+`","operations":[{"action":"add","content":"fact for `+target+`"}]}`), ctx)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if !strings.Contains(out, `"mode":"`+title+`"`) {
			t.Fatalf("%s mode title missing: %s", target, out)
		}
	}
	// Each mode holds only its own entry.
	for target := range want {
		out, err := tool.Call(json.RawMessage(`{"target":"`+target+`"}`), ctx)
		if err != nil {
			t.Fatalf("%s read: %v", target, err)
		}
		for other := range want {
			if other == target {
				continue
			}
			if strings.Contains(out, "fact for "+other) {
				t.Fatalf("%s leaked into %s: %s", other, target, out)
			}
		}
		if !strings.Contains(out, "fact for "+target) {
			t.Fatalf("%s missing its entry: %s", target, out)
		}
	}
}

// Workspace rules need a folder; user and global do not.
func TestWorkspaceRulesNeedAFolder(t *testing.T) {
	bank := NewBank(t.TempDir())
	bank.SetConfig(bank.Config())
	if _, err := bank.Read(TargetProject, ""); err == nil {
		t.Fatal("workspace rules without a folder must fail")
	}
	if _, err := bank.Read(TargetUser, ""); err != nil {
		t.Fatalf("user mode must not need a folder: %v", err)
	}
	if _, err := bank.Read(TargetGlobal, ""); err != nil {
		t.Fatalf("global mode must not need a folder: %v", err)
	}
}
