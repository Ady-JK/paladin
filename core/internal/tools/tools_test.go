package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCtx(t *testing.T) (*ToolCtx, string) {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return &ToolCtx{Workspace: ws, SessionDir: filepath.Join(dir, "sess"), Cancel: context.Background()}, ws
}

func TestUnknownToolIsErrorResult(t *testing.T) {
	r := NewRegistry()
	result := r.Call(&ToolCall{ID: "1", Name: "missing", Arguments: "{}"}, &ToolCtx{})
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestDefinitionsSortedByName(t *testing.T) {
	r := NewRegistry()
	r.Register(ReadTool{})
	if len(r.Definitions()) != 1 {
		t.Fatal("expected one definition")
	}
	if got := BuiltinRegistry().Len(); got != 6 {
		t.Fatalf("expected 6 builtin tools, got %d", got)
	}
}

func TestToolErrorStrings(t *testing.T) {
	cases := []struct {
		err  *ToolError
		want string
	}{
		{&ToolError{Kind: "unknown", Msg: "x"}, "unknown tool: x"},
		{&ToolError{Kind: "args", Msg: "bad"}, "bad arguments: bad"},
		{&ToolError{Kind: "exec", Msg: "fail"}, "execution failed: fail"},
		{&ToolError{Kind: "failed", Msg: "m", Content: "c"}, "m"},
		{&ToolError{Kind: "other", Msg: "m"}, "m"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
	if got := WithContent("c", "m").ResultContent(); got != "c" {
		t.Fatalf("got %q", got)
	}
	if got := ArgsError("bad").ResultContent(); !strings.Contains(got, "bad") {
		t.Fatalf("got %q", got)
	}
	plain := &ToolError{Kind: "args", Msg: "bad"}
	if got := plain.ResultContent(); !strings.Contains(got, "bad arguments") {
		t.Fatalf("got %q", got)
	}
	// Non-ToolError results are wrapped as JSON.
	r := NewRegistry()
	r.Register(ReadTool{})
	res := r.Call(&ToolCall{ID: "1", Name: "read", Arguments: "{"}, &ToolCtx{})
	if !res.IsError || !strings.Contains(res.Content, "valid JSON") {
		t.Fatalf("got %+v", res)
	}
	// Blank arguments become null -> read complains about path.
	res = r.Call(&ToolCall{ID: "1", Name: "read", Arguments: "   "}, &ToolCtx{Workspace: t.TempDir()})
	if !res.IsError {
		t.Fatalf("got %+v", res)
	}
}

func TestRegistryDefinitionsSorted(t *testing.T) {
	r := NewRegistry()
	r.Register(NewShellTool())
	r.Register(ReadTool{})
	defs := r.Definitions()
	if len(defs) != 2 || defs[0].Name != "read" || defs[1].Name != "shell" {
		t.Fatalf("got %v", defs)
	}
	if got := NewRegistry().Definitions(); got == nil || len(got) != 0 {
		t.Fatalf("empty registry should give [], got %+v", got)
	}
	if got := r.Names(); len(got) != 2 || got[0] != "read" || got[1] != "shell" {
		t.Fatalf("got %v", got)
	}
	if _, ok := r.Get("read"); !ok {
		t.Fatal("Get failed")
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get false positive")
	}
}

func TestRegistrySubsetSharesInstances(t *testing.T) {
	reg := BuiltinRegistry()
	shell, _ := reg.Get("shell")
	sub, err := reg.Subset([]string{"shell", "read"})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Len() != 2 {
		t.Fatalf("got %d", sub.Len())
	}
	got, ok := sub.Get("shell")
	if !ok || got != shell {
		t.Fatal("subset must share tool instances")
	}
	// Empty names clones everything.
	all, err := reg.Subset(nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Len() != reg.Len() {
		t.Fatalf("got %d", all.Len())
	}
	// Unknown names fail loudly.
	if _, err := reg.Subset([]string{"raed"}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("got %v", err)
	}
}

func TestCatalogRegistersAndInstantiates(t *testing.T) {
	c := BuiltinCatalog()
	names := c.Names()
	want := []string{"ask", "edit", "read", "shell", "todo", "write"}
	if len(names) != len(want) {
		t.Fatalf("got %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v", names)
		}
	}
	reg := c.Instantiate()
	if reg.Len() != 6 {
		t.Fatalf("got %d", reg.Len())
	}
	for _, n := range want {
		if _, ok := reg.Get(n); !ok {
			t.Fatalf("missing %q", n)
		}
	}
	// Each instantiate makes fresh instances, so background jobs do not
	// leak between registries.
	a := c.Instantiate()
	b := c.Instantiate()
	aShell, _ := a.Get("shell")
	bShell, _ := b.Get("shell")
	if aShell == bShell {
		t.Fatal("catalog should build independent tool instances")
	}
}

func TestCatalogIgnoresInvalidTemplates(t *testing.T) {
	c := NewCatalog()
	c.Register(Template{Name: "", New: func() Tool { return ReadTool{} }})
	c.Register(Template{Name: "nil"})
	if names := c.Names(); len(names) != 0 {
		t.Fatalf("got %v", names)
	}
}

func TestRegistryFilter(t *testing.T) {
	r := BuiltinRegistry()
	keep := map[string]bool{"read": true, "todo": true}
	out := r.Filter(func(name string) bool { return keep[name] })
	if got := out.Names(); len(got) != 2 || got[0] != "read" || got[1] != "todo" {
		t.Fatalf("%v", got)
	}
	if none := r.Filter(func(string) bool { return false }); none.Len() != 0 {
		t.Fatalf("filtering everything out must leave nothing: %v", none.Names())
	}
	if r.Len() != len(BuiltinCatalog().Names()) {
		t.Fatal("filter must not change the source registry")
	}
}
