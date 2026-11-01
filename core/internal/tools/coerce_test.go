package tools

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

var coerceSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"path":   map[string]any{"type": "string"},
		"offset": map[string]any{"type": "integer"},
		"ratio":  map[string]any{"type": "number"},
		"all":    map[string]any{"type": "boolean"},
		"note":   map[string]any{"type": []string{"string", "null"}},
		"label":  map[string]any{"type": "string"},
		"choice": map[string]any{"anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}},
		"opts":   map[string]any{"type": "object", "properties": map[string]any{"depth": map[string]any{"type": "integer"}}},
		"todos": map[string]any{"type": "array", "items": map[string]any{
			"type":       "object",
			"properties": map[string]any{"done": map[string]any{"type": "boolean"}},
		}},
	},
	"required": []string{"path", "label"},
}

func coerced(t *testing.T, in string) map[string]any {
	t.Helper()
	out := coerceArgs(json.RawMessage(in), coerceSchema, true)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("coerce %s: %v", in, err)
	}
	return got
}

func TestCoerceFixesCommonModelSlips(t *testing.T) {
	got := coerced(t, `{"path":"/a","label":7,"offset":"20","ratio":"0.5","all":"TRUE",
		"opts":"{\"depth\":\"3\"}","todos":"[{\"done\":\"false\"}]","note":null}`)
	want := map[string]any{
		"path": "/a", "label": "7", "offset": float64(20), "ratio": 0.5, "all": true,
		"opts":  map[string]any{"depth": float64(3)},
		"todos": []any{map[string]any{"done": false}},
		"note":  nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestCoerceDropsNullPlaceholdersOnlyWhenOptional(t *testing.T) {
	got := coerced(t, `{"path":"/a","label":null,"offset":null,"all":null}`)
	if _, ok := got["offset"]; ok {
		t.Error("optional null offset should be dropped")
	}
	if _, ok := got["all"]; ok {
		t.Error("optional null all should be dropped")
	}
	if v, ok := got["label"]; !ok || v != nil {
		t.Error("a required null is left for the tool to reject")
	}
}

func TestCoerceLeavesAmbiguousValuesAlone(t *testing.T) {
	for _, in := range []string{
		`{"path":"/a","label":"x","offset":"2.5"}`,
		`{"path":"/a","label":"x","offset":"twenty"}`,
		`{"path":"/a","label":"x","all":"yes"}`,
		`{"path":"/a","label":"x","choice":"5"}`,
		`{"path":"/a","label":"x","opts":"not json"}`,
		`{"path":"/a","label":"x","extra":"kept"}`,
	} {
		out := coerceArgs(json.RawMessage(in), coerceSchema, true)
		if string(out) != in {
			t.Errorf("%s changed to %s", in, out)
		}
	}
	if out := coerceArgs(json.RawMessage(`[1,2]`), coerceSchema, true); string(out) != `[1,2]` {
		t.Errorf("non-object arguments changed: %s", out)
	}
	raw := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`)
	if out := coerceArgs(json.RawMessage(`{"n":"4"}`), raw, true); string(out) != `{"n":4}` {
		t.Errorf("a raw JSON schema (extension tools) should coerce too: %s", out)
	}
	if out := coerceArgs(json.RawMessage(`{"a":"1"}`), nil, true); string(out) != `{"a":"1"}` {
		t.Errorf("no schema changed: %s", out)
	}
}

func TestRegistryCoercesBeforeTheToolAndTheGate(t *testing.T) {
	ctx, ws := testCtx(t)
	writeTree(t, ws, map[string]string{"a.txt": "l1\nl2\nl3\n"})
	r := BuiltinRegistry()
	args := `{"path":"` + ws + `/a.txt","offset":"2","limit":"1"}`
	res := r.Call(&ToolCall{ID: "1", Name: "read", Arguments: args}, ctx)
	var resp ReadResponse
	if res.IsError || json.Unmarshal([]byte(res.Content), &resp) != nil || resp.Content != "l2\n" {
		t.Fatalf("string numbers should work: %+v", res)
	}
	_, prepared, ok := r.Prepare(&ToolCall{Name: "read", Arguments: args})
	var p map[string]any
	if !ok || json.Unmarshal(prepared, &p) != nil || p["offset"] != float64(2) {
		t.Fatalf("the gate must judge the coerced arguments: %s", prepared)
	}
}

func TestCoerceRecoversALeakedParameterTag(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"verdict": map[string]any{"type": "string"},
		"note":    map[string]any{"type": "string"},
		"count":   map[string]any{"type": "integer"},
	}, "required": []string{"summary", "verdict"}}
	in := `{"summary":"Two bugs.\",\n<parameter name=\"verdict\">needs_rework"}`
	var got map[string]any
	if err := json.Unmarshal(coerceArgs(json.RawMessage(in), schema, true), &got); err != nil {
		t.Fatal(err)
	}
	if got["summary"] != "Two bugs." || got["verdict"] != "needs_rework" {
		t.Fatalf("got %#v", got)
	}
	// A leak naming a field the call already has, one the schema does
	// not know, or an optional one (no evidence the call is broken) is
	// left alone.
	for _, in := range []string{
		`{"summary":"x\",\n<parameter name=\"verdict\">a","verdict":"b"}`,
		`{"summary":"x\",\n<parameter name=\"other\">a"}`,
		`{"summary":"x","verdict":"a","note":"y\",\n<parameter name=\"count\">3"}`,
	} {
		if out := coerceArgs(json.RawMessage(in), schema, true); string(out) != in {
			t.Errorf("%s changed to %s", in, out)
		}
	}
	in = `{"summary":"ok","count":"<parameter name=\"x\">"}`
	if out := coerceArgs(json.RawMessage(in), schema, true); string(out) != in {
		t.Errorf("a non-leak is untouched: %s", out)
	}
}

// Only the slip itself is undone: the closing quote and comma the model
// meant to send, then one tag whose value is a single plain line.
// Anything else that merely looks like a tag stays as sent.
func TestCoerceRecoversOnlyAnUnmistakableLeak(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"verdict": map[string]any{"type": "string"},
	}, "required": []string{"summary", "verdict"}}
	for _, in := range []string{
		// No closing quote and comma: text that quotes a tag.
		`{"summary":"Use <parameter name=\"verdict\">needs_rework"}`,
		// Two tags.
		`{"summary":"a\",\n<parameter name=\"verdict\">x\",\n<parameter name=\"verdict\">y"}`,
		// A value over several lines.
		`{"summary":"a\",\n<parameter name=\"verdict\">x\ny"}`,
		// Markup in the value.
		`{"summary":"a\",\n<parameter name=\"verdict\"><b>x</b>"}`,
	} {
		if out := coerceArgs(json.RawMessage(in), schema, true); string(out) != in {
			t.Errorf("%s changed to %s", in, out)
		}
	}
	leak := `{"summary":"a\",\n<parameter name=\"verdict\">ok"}`
	if out := coerceArgs(json.RawMessage(leak), schema, false); string(out) != leak {
		t.Fatalf("recovery off still recovered: %s", out)
	}
	if out := coerceArgs(json.RawMessage(leak), schema, true); !strings.Contains(string(out), `"verdict":"ok"`) {
		t.Fatalf("the slip itself is recovered: %s", out)
	}
}

// A write whose content ends with what looks like a leaked path is not
// turned into a write to that path: the call fails as sent.
func TestCoerceNeverInventsAPathForAWrite(t *testing.T) {
	ctx, ws := testCtx(t)
	r := BuiltinRegistry()
	target := ws + "/elsewhere.txt"
	content := "notes\",\n<parameter name=\"path\">" + target
	args, _ := json.Marshal(map[string]any{"content": content})
	_, prepared, _ := r.Prepare(&ToolCall{Name: "write", Arguments: string(args)})
	if string(prepared) != string(args) {
		t.Fatalf("the gate saw changed arguments: %s", prepared)
	}
	if res := r.Call(&ToolCall{ID: "1", Name: "write", Arguments: string(args)}, ctx); !res.IsError {
		t.Fatalf("a write without a path ran: %s", res.Content)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a path was taken from the content: %v", err)
	}
}

// Valid file content that merely contains parameter-like text is
// written exactly as sent, and never turns on another option.
func TestCoerceKeepsParameterLikeFileContent(t *testing.T) {
	ctx, ws := testCtx(t)
	r := BuiltinRegistry()
	content := "Example of a tool call:\n<parameter name=\"overwrite\">true"
	args, _ := json.Marshal(map[string]any{"path": ws + "/doc.txt", "content": content})
	if res := r.Call(&ToolCall{ID: "1", Name: "write", Arguments: string(args)}, ctx); res.IsError {
		t.Fatalf("write: %s", res.Content)
	}
	if got, _ := os.ReadFile(ws + "/doc.txt"); string(got) != content {
		t.Fatalf("written %q", got)
	}
	_, prepared, _ := r.Prepare(&ToolCall{Name: "write", Arguments: string(args)})
	if string(prepared) != string(args) {
		t.Fatalf("the gate saw %s", prepared)
	}

	newText := "x\",\n<parameter name=\"replace_all\">true"
	edit, _ := json.Marshal(map[string]any{"path": ws + "/doc.txt", "old_string": "Example", "new_string": newText})
	if res := r.Call(&ToolCall{ID: "2", Name: "edit", Arguments: string(edit)}, ctx); res.IsError {
		t.Fatalf("edit: %s", res.Content)
	}
	if got, _ := os.ReadFile(ws + "/doc.txt"); !strings.HasPrefix(string(got), newText+" of a tool call") {
		t.Fatalf("edited %q", got)
	}
}

// Integers beyond float64's exact range reach the tool and the gate
// unchanged.
func TestCoerceKeepsLargeIntegersExact(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"n": map[string]any{"type": "integer"}, "s": map[string]any{"type": "string"},
	}}
	for _, n := range []string{"9007199254740992", "9007199254740993", "9223372036854775807", "-9223372036854775808"} {
		if out := string(coerceArgs(json.RawMessage(`{"n":"`+n+`"}`), schema, true)); out != `{"n":`+n+`}` {
			t.Errorf("%s became %s", n, out)
		}
		// A number already in the call keeps its digits when another
		// field is fixed.
		in := `{"n":` + n + `,"s":1}`
		if out := string(coerceArgs(json.RawMessage(in), schema, true)); out != `{"n":`+n+`,"s":"1"}` {
			t.Errorf("%s became %s", in, out)
		}
		if out := string(coerceArgs(json.RawMessage(`{"s":`+n+`}`), schema, true)); out != `{"s":"`+n+`"}` {
			t.Errorf("string %s became %s", n, out)
		}
	}
	// Past the signed 64-bit range it stays a string for the tool to reject.
	in := `{"n":"9223372036854775808"}`
	if out := string(coerceArgs(json.RawMessage(in), schema, true)); out != in {
		t.Errorf("out of range became %s", out)
	}
}
