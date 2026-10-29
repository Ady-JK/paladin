package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

type panicTool struct{}

func (panicTool) Name() string { return "boom" }
func (panicTool) Definition() ToolDefinition {
	return ToolDefinition{Name: "boom", Parameters: map[string]any{"type": "object"}}
}
func (panicTool) Call(json.RawMessage, *ToolCtx) (string, error) {
	panic("boom")
}

type echoTool struct{ name string }

func (e echoTool) Name() string { return e.name }
func (e echoTool) Definition() ToolDefinition {
	return ToolDefinition{Name: e.name, Parameters: map[string]any{"type": "object"}}
}
func (echoTool) Call(args json.RawMessage, _ *ToolCtx) (string, error) { return string(args), nil }

func TestCallRecoversToolPanic(t *testing.T) {
	reg := NewRegistry()
	reg.Register(panicTool{})
	res := reg.Call(&ToolCall{ID: "c1", Name: "boom", Arguments: "{}"}, &ToolCtx{})
	if !res.IsError || !strings.Contains(res.Content, "crashed") || res.CallID != "c1" {
		t.Fatalf("panic must become an error result, got %+v", res)
	}
	// The registry stays usable afterwards.
	reg.Register(echoTool{name: "echo"})
	if r := reg.Call(&ToolCall{ID: "c2", Name: "echo", Arguments: `{"a":1}`}, &ToolCtx{}); r.IsError {
		t.Fatalf("registry broken after panic: %+v", r)
	}
}

func TestCallNormalizesToolNames(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool{name: "web_fetch"})
	for _, name := range []string{"web_fetch", " web_fetch ", "Web_Fetch", "functions.web_fetch", "web-fetch", "tools.web_fetch"} {
		res := reg.Call(&ToolCall{ID: "c", Name: name, Arguments: "{}"}, &ToolCtx{})
		if res.IsError {
			t.Fatalf("%q should resolve to web_fetch: %+v", name, res)
		}
		if res.Name != "web_fetch" {
			t.Fatalf("%q resolved name = %q", name, res.Name)
		}
	}
}

func TestUnknownToolListsAvailable(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool{name: "read"})
	reg.Register(echoTool{name: "write"})
	res := reg.Call(&ToolCall{ID: "c", Name: "delete_everything", Arguments: "{}"}, &ToolCtx{})
	if !res.IsError || !strings.Contains(res.Content, "read") || !strings.Contains(res.Content, "write") {
		t.Fatalf("unknown tool must list available tools: %+v", res)
	}
}

func TestCallRepairsLightJSONDamage(t *testing.T) {
	reg := NewRegistry()
	reg.Register(echoTool{name: "echo"})
	cases := map[string]string{
		"```json\n{\"path\":\"a\"}\n```": `{"path":"a"}`,
		"```\n{\"path\":\"a\"}\n```":     `{"path":"a"}`,
		`{"path":"a",}`:                  `{"path":"a"}`,
		`{"list":[1,2,],}`:               `{"list":[1,2]}`,
		`{"s":"keep ,} inside"}`:         `{"s":"keep ,} inside"}`,
		"  {\"path\":\"a\"}  ":           `{"path":"a"}`,
	}
	for in, want := range cases {
		res := reg.Call(&ToolCall{ID: "c", Name: "echo", Arguments: in}, &ToolCtx{})
		if res.IsError {
			t.Fatalf("%q should be repaired: %+v", in, res)
		}
		var got, exp any
		if err := json.Unmarshal([]byte(res.Content), &got); err != nil {
			t.Fatalf("%q: tool saw invalid JSON %q", in, res.Content)
		}
		_ = json.Unmarshal([]byte(want), &exp)
		gb, _ := json.Marshal(got)
		eb, _ := json.Marshal(exp)
		if string(gb) != string(eb) {
			t.Fatalf("%q: got %s want %s", in, gb, eb)
		}
	}
	res := reg.Call(&ToolCall{ID: "c", Name: "echo", Arguments: `{"path": "a"`}, &ToolCtx{})
	if !res.IsError || !strings.Contains(res.Content, "not valid JSON") {
		t.Fatalf("truncated JSON must still fail with guidance: %+v", res)
	}
}
