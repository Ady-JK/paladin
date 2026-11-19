package tools

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type mcpProbe struct{ echoTool }

func (mcpProbe) MCPInfo() MCPInfo { return MCPInfo{Server: "s", Tool: "t"} }

func TestEveryBuiltinToolRequiresAReason(t *testing.T) {
	reg := BuiltinRegistry()
	defs := reg.Definitions()
	if len(defs) == 0 {
		t.Fatal("no builtin tools")
	}
	for _, def := range defs {
		schema, ok := def.Parameters.(map[string]any)
		if !ok {
			t.Fatalf("%s: parameters %T", def.Name, def.Parameters)
		}
		prop, _ := schema["properties"].(map[string]any)[ReasonParam].(map[string]any)
		if prop == nil || prop["type"] != "string" || prop["description"] == "" {
			t.Errorf("%s: reason property = %v", def.Name, prop)
		}
		if required := stringList(schema["required"]); !slices.Contains(required, ReasonParam) {
			t.Errorf("%s: reason is not required: %v", def.Name, required)
		}
	}
}

func TestCallRefusesAMissingOrBlankReason(t *testing.T) {
	ctx, ws := testCtx(t)
	writeTree(t, ws, map[string]string{"a.txt": "hi\n"})
	reg := BuiltinRegistry()
	for _, args := range []string{
		`{"path":"` + ws + `/a.txt"}`,
		`{"path":"` + ws + `/a.txt","reason":""}`,
		`{"path":"` + ws + `/a.txt","reason":"   "}`,
		``,
	} {
		call := &ToolCall{ID: "1", Name: "read", Arguments: args}
		if res := reg.Call(call, ctx); !res.IsError || !strings.Contains(res.Content, "missing required argument") {
			t.Errorf("%q: want a missing-reason error, got %+v", args, res)
		}
		if res, refused := reg.CheckReason(call); !refused || !res.IsError {
			t.Errorf("%q: CheckReason did not refuse", args)
		}
	}
	ok := &ToolCall{ID: "1", Name: "read", Arguments: `{"path":"` + ws + `/a.txt","reason":"Reading a.txt to check it"}`}
	if res := reg.Call(ok, ctx); res.IsError {
		t.Fatalf("a call with a reason failed: %s", res.Content)
	}
	if _, refused := reg.CheckReason(ok); refused {
		t.Fatal("CheckReason refused a call with a reason")
	}
}

// The tool never sees the reason, so tools that decode strictly keep working.
func TestCallStripsTheReasonBeforeTheTool(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool{name: "echo"})
	res := reg.Call(&ToolCall{ID: "c", Name: "echo", Arguments: `{"a":1,"reason":"why"}`}, &ToolCtx{})
	if res.IsError || strings.Contains(res.Content, "reason") {
		t.Fatalf("tool saw the reason: %+v", res)
	}
}

func TestMCPToolsTakeNoReason(t *testing.T) {
	reg := NewRegistry()
	reg.Register(mcpProbe{echoTool{name: "mcp__s__t"}})
	for _, def := range reg.Definitions() {
		if props, _ := def.Parameters.(map[string]any)["properties"].(map[string]any); props != nil && props[ReasonParam] != nil {
			t.Errorf("%s got a reason property", def.Name)
		}
	}
	for _, name := range []string{"mcp__s__t"} {
		if res := reg.Call(&ToolCall{ID: "c", Name: name, Arguments: `{"a":1}`}, &ToolCtx{}); res.IsError {
			t.Errorf("%s: %s", name, res.Content)
		}
	}
}

func TestDefinitionsDoNotMutateTheToolSchema(t *testing.T) {
	tool := ReadTool{}
	before, _ := json.Marshal(tool.Definition().Parameters)
	reg := NewRegistry()
	reg.Register(tool)
	reg.Definitions()
	after, _ := json.Marshal(tool.Definition().Parameters)
	if string(before) != string(after) || strings.Contains(string(after), ReasonParam) {
		t.Fatalf("schema changed: %s", after)
	}
}

func TestKeepReasonAndDefaultReason(t *testing.T) {
	reg := BuiltinRegistry()
	call := &ToolCall{Name: "read", Arguments: `{"path":"a","reason":"orig"}`}
	if got := reasonOf(json.RawMessage(reg.KeepReason(call, json.RawMessage(`{"path":"b"}`)))); got != "orig" {
		t.Fatalf("reason after rewrite = %q", got)
	}
	if got := reasonOf(json.RawMessage(reg.KeepReason(call, json.RawMessage(`{"path":"b","reason":"new"}`)))); got != "new" {
		t.Fatalf("a hook's own reason was replaced: %q", got)
	}
	for _, in := range []string{``, `null`, `{}`, `{"path":"a"}`} {
		if got := reasonOf(WithDefaultReason(json.RawMessage(in), "panel")); got != "panel" {
			t.Errorf("%q: default reason = %q", in, got)
		}
	}
	if got := reasonOf(WithDefaultReason(json.RawMessage(`{"reason":"mine"}`), "panel")); got != "mine" {
		t.Fatalf("existing reason replaced: %q", got)
	}
}

func TestWithoutReasonMakesCallsComparable(t *testing.T) {
	reg := BuiltinRegistry()
	a := reg.WithoutReason("read", `{"path":"a","reason":"one"}`)
	b := reg.WithoutReason("read", `{"path":"a","reason":"two"}`)
	if a != b || strings.Contains(a, "reason") {
		t.Fatalf("%s vs %s", a, b)
	}
}
