// todo_test.go covers the todo tool: list creation, status
// transitions, the exactly-one-in_progress rule, invalid input errors
// (all model-visible, never a crash), and persistence across registry
// instances for the same session.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func todoCtx(t *testing.T) *ToolCtx {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sess"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &ToolCtx{Workspace: dir, SessionDir: filepath.Join(dir, "sess"), Cancel: context.Background()}
}

func callTodo(ctx *ToolCtx, args string) (string, error) {
	return TodoTool{}.Call(json.RawMessage(args), ctx)
}

func decodeTodos(t *testing.T, content string) []TodoItem {
	t.Helper()
	var out struct {
		Todos []TodoItem `json:"todos"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		t.Fatalf("decode todos %q: %v", content, err)
	}
	return out.Todos
}

func TestTodoCreateList(t *testing.T) {
	ctx := todoCtx(t)
	content, err := callTodo(ctx, `{"todos":[
		{"content":"Implement the todo tool","status":"in_progress"},
		{"content":"Add tests","status":"pending"},
		{"content":"Run checks","status":"pending"}]}`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got := decodeTodos(t, content)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Status != TodoInProgress || got[1].Status != TodoPending {
		t.Fatalf("got %+v", got)
	}
}

func TestTodoStatusTransitions(t *testing.T) {
	ctx := todoCtx(t)
	if _, err := callTodo(ctx, `{"todos":[
		{"content":"One","status":"in_progress"},
		{"content":"Two","status":"pending"}]}`); err != nil {
		t.Fatal(err)
	}
	// Finish the first, start the second: the whole list is replaced.
	content, err := callTodo(ctx, `{"todos":[
		{"content":"One","status":"completed"},
		{"content":"Two","status":"in_progress"}]}`)
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	got := decodeTodos(t, content)
	if got[0].Status != TodoCompleted || got[1].Status != TodoInProgress {
		t.Fatalf("got %+v", got)
	}
	// Cancel the remainder.
	content, err = callTodo(ctx, `{"todos":[
		{"content":"One","status":"completed"},
		{"content":"Two","status":"cancelled"}]}`)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := decodeTodos(t, content); got[1].Status != TodoCancelled {
		t.Fatalf("got %+v", got)
	}
	// Read mode (no todos key) returns the stored list.
	content, err = callTodo(ctx, `{}`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got = decodeTodos(t, content)
	if len(got) != 2 || got[0].Status != TodoCompleted || got[1].Status != TodoCancelled {
		t.Fatalf("read got %+v", got)
	}
	// Reading before any write is an empty list, not an error.
	fresh := todoCtx(t)
	content, err = callTodo(fresh, `{"kind":"ignored"}`)
	if err != nil {
		t.Fatalf("empty read: %v", err)
	}
	if got := decodeTodos(t, content); len(got) != 0 {
		t.Fatalf("empty read got %+v", got)
	}
}

func TestTodoExactlyOneInProgress(t *testing.T) {
	ctx := todoCtx(t)
	_, err := callTodo(ctx, `{"todos":[
		{"content":"One","status":"in_progress"},
		{"content":"Two","status":"in_progress"}]}`)
	if err == nil {
		t.Fatal("two in_progress accepted")
	}
	if !strings.Contains(err.Error(), "in_progress") {
		t.Fatalf("error does not explain the rule: %v", err)
	}
	// A rejected write stores nothing.
	content, err := callTodo(ctx, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeTodos(t, content); len(got) != 0 {
		t.Fatalf("rejected write left %+v", got)
	}
	// Zero in_progress is fine.
	if _, err := callTodo(ctx, `{"todos":[{"content":"One","status":"pending"}]}`); err != nil {
		t.Fatalf("no in_progress rejected: %v", err)
	}
}

func TestTodoInvalidInput(t *testing.T) {
	ctx := todoCtx(t)
	for name, args := range map[string]string{
		"empty list":    `{"todos":[]}`,
		"blank content": `{"todos":[{"content":"  ","status":"pending"}]}`,
		"bad status":    `{"todos":[{"content":"One","status":"doing"}]}`,
		"missing keys":  `{"todos":[{}]}`,
		"no session":    ``,
	} {
		var err error
		if name == "no session" {
			_, err = TodoTool{}.Call(json.RawMessage(`{"todos":[{"content":"One","status":"pending"}]}`), &ToolCtx{})
		} else {
			_, err = callTodo(ctx, args)
		}
		if err == nil {
			t.Fatalf("%s accepted", name)
		}
		if _, ok := err.(*ToolError); !ok {
			t.Fatalf("%s: got %T(%v), want *ToolError", name, err, err)
		}
	}
	// Bad JSON is a model-visible error through the registry, not a panic.
	// (Registry-level JSON errors are generic and carry no todo: prefix.)
	r := NewRegistry()
	r.Register(TodoTool{})
	res := r.Call(&ToolCall{ID: "1", Name: "todo", Arguments: `{"todos":`}, ctx)
	if !res.IsError || !strings.Contains(res.Content, "valid JSON") {
		t.Fatalf("got %+v", res)
	}
}

func TestTodoPersistsAcrossRegistries(t *testing.T) {
	dir := t.TempDir()
	sess := filepath.Join(dir, "sess")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := &ToolCtx{Workspace: dir, SessionDir: sess, Cancel: context.Background()}

	// Turn one, through one registry instance: create the list.
	regA := BuiltinRegistry()
	res := regA.Call(&ToolCall{ID: "1", Name: "todo", Arguments: `{"todos":[
		{"content":"One","status":"in_progress"},
		{"content":"Two","status":"pending"}]}`}, ctx)
	if res.IsError {
		t.Fatalf("write: %s", res.Content)
	}

	// Turn two, through a fresh registry instance (new tool objects):
	// the list is still there, in the session dir next to the transcript.
	regB := BuiltinRegistry()
	if _, ok := regB.Get("todo"); !ok {
		t.Fatal("todo missing from builtin registry")
	}
	res = regB.Call(&ToolCall{ID: "2", Name: "todo", Arguments: `{}`}, ctx)
	if res.IsError {
		t.Fatalf("read: %s", res.Content)
	}
	got := decodeTodos(t, res.Content)
	if len(got) != 2 || got[0].Content != "One" || got[0].Status != TodoInProgress {
		t.Fatalf("got %+v", got)
	}

	// The file sits in the session dir with owner-only permissions.
	st, err := os.Stat(filepath.Join(sess, TodoFileName))
	if err != nil {
		t.Fatalf("todos.json missing: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("todos.json perms %o, want 600", st.Mode().Perm())
	}
}
