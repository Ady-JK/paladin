// Package tools: todo.go tracks a goal-based activity list per session.
//
// The model plans multi-step work first (one todo call with the whole
// list), keeps exactly one step in_progress at a time, and updates
// statuses as it works. State lives in the session dir next to
// messages.jsonl/usage.json (todos.json), written atomically with an
// fsync like the transcript, so the list survives across turns and
// across registry instances: the tool itself is stateless and every
// call loads from (or writes to) disk.
package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

// Todo statuses. At most one todo may be in_progress at a time.
const (
	TodoNotStarted = "not_started"
	TodoInProgress = "in_progress"
	TodoFinished   = "finished"
	TodoCancelled  = "cancelled"
)

// TodoFileName is the per-session activity list. It lives in the
// session dir next to meta.json/messages.jsonl/usage.json. The name is
// shared with the session package's view helper.
const TodoFileName = session.TodoFileName

// TodoItem is one planned activity: what to do and where it stands.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// TodoTool replaces (or reads) the session's activity list. It is
// stateless: every call goes through the session dir, so no shared
// instance is needed across turns (unlike the shell tool, whose live
// jobs live in memory).
type TodoTool struct{}

func (TodoTool) Name() string { return "todo" }

func (TodoTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "todo",
		Description: "REQUIRED for multi-step work: break the task into a " +
			"goal-based activity list BEFORE starting, update the statuses " +
			"as you work, and keep exactly one step in_progress at a time. " +
			"Every step you are working on MUST be marked in_progress, and " +
			"every step you finish MUST be marked finished (or cancelled) " +
			"before you move on — never leave a step in_progress once its " +
			"work is done. A finished plan is cleared when the user sends " +
			"their next message; plan the new work with a fresh list when " +
			"it is multi-step. Pass todos to replace the session list; omit " +
			"todos to read the current list.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "The activity list, replacing the session list. Omit to read the current list.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{"type": "string", "description": "What to do; one goal-based step."},
							"status":  map[string]any{"type": "string", "description": "Where the step stands.", "enum": []string{TodoNotStarted, TodoInProgress, TodoFinished, TodoCancelled}},
						},
						"required": []string{"content", "status"},
					},
				},
			},
		},
	}
}

func (TodoTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		// Pointer so read mode (key absent/null) differs from a
		// write of an empty list (key present but empty).
		Todos *[]TodoItem `json:"todos"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("todo: the arguments are not valid JSON: %v", err))
	}
	var sessionDir string
	if ctx != nil {
		sessionDir = strings.TrimSpace(ctx.SessionDir)
	}
	if sessionDir == "" {
		return "", ArgsError("todo: no session to keep todos in")
	}
	if args.Todos == nil {
		list, err := loadTodos(sessionDir)
		if err != nil {
			return "", err
		}
		data, merr := jsonx.Marshal(map[string]any{"todos": list})
		if merr != nil {
			return "", ArgsError(fmt.Sprintf("todo: encode the result: %v", merr))
		}
		return string(data), nil
	}
	normal, err := validateTodos(*args.Todos)
	if err != nil {
		return "", err
	}
	if err := saveTodos(sessionDir, normal); err != nil {
		return "", err
	}
	data, merr := jsonx.Marshal(map[string]any{"todos": normal})
	if merr != nil {
		return "", ArgsError(fmt.Sprintf("todo: encode the result: %v", merr))
	}
	return string(data), nil
}

// legacyTodoStatus maps the previous status names (pending,
// completed) to their renames so stored lists and older prompts keep
// working.
func legacyTodoStatus(status string) (string, bool) {
	switch status {
	case "pending":
		return TodoNotStarted, true
	case "completed":
		return TodoFinished, true
	default:
		return "", false
	}
}

// validateTodos trims, checks statuses, and enforces at most one
// in_progress. Every rejection is a model-visible ArgsError, never a
// crash: bad input comes back as an error tool result.
func validateTodos(todos []TodoItem) ([]TodoItem, error) {
	if len(todos) == 0 {
		return nil, ArgsError("todo: todos is required and must not be empty")
	}
	normal := make([]TodoItem, 0, len(todos))
	inProgress := 0
	for i, td := range todos {
		content := strings.TrimSpace(td.Content)
		status := strings.TrimSpace(td.Status)
		if content == "" {
			return nil, ArgsError(fmt.Sprintf("todo: item %d content must not be empty", i+1))
		}
		if legacy, ok := legacyTodoStatus(status); ok {
			status = legacy
		}
		switch status {
		case TodoNotStarted, TodoInProgress, TodoFinished, TodoCancelled:
		default:
			return nil, ArgsError(fmt.Sprintf("todo: item %d status must be one of not_started, in_progress, finished or cancelled, got %q", i+1, td.Status))
		}
		if status == TodoInProgress {
			inProgress++
		}
		normal = append(normal, TodoItem{Content: content, Status: status})
	}
	if inProgress > 1 {
		return nil, ArgsError(fmt.Sprintf("todo: only one todo may be in_progress at a time, got %d", inProgress))
	}
	return normal, nil
}

func todoPath(sessionDir string) string {
	return filepath.Join(sessionDir, TodoFileName)
}

// loadTodos reads the stored list; a missing file is an empty list,
// so reading before the first write works.
func loadTodos(sessionDir string) ([]TodoItem, error) {
	data, err := os.ReadFile(todoPath(sessionDir))
	if err != nil {
		if os.IsNotExist(err) {
			return []TodoItem{}, nil
		}
		return nil, ArgsError(fmt.Sprintf("todo: cannot read the list: %s", syserr.Str(err)))
	}
	var stored struct {
		Todos []TodoItem `json:"todos"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, ArgsError(fmt.Sprintf("todo: stored list is corrupt: %v", err))
	}
	if stored.Todos == nil {
		return []TodoItem{}, nil
	}
	return stored.Todos, nil
}

// saveTodos persists the list atomically: temp file in the session
// dir plus rename, with an fsync before the rename like the
// transcript appends. Session files may hold workspace plans, so they
// are owner-only.
func saveTodos(sessionDir string, todos []TodoItem) error {
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return ArgsError(fmt.Sprintf("todo: cannot keep the list: %s", syserr.Str(err)))
	}
	data, err := jsonx.Marshal(map[string]any{"todos": todos})
	if err != nil {
		return ArgsError(fmt.Sprintf("todo: encode the list: %v", err))
	}
	tmp, err := os.CreateTemp(sessionDir, "todos-*.tmp")
	if err != nil {
		return ArgsError(fmt.Sprintf("todo: cannot keep the list: %s", syserr.Str(err)))
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return ArgsError(fmt.Sprintf("todo: cannot keep the list: %s", syserr.Str(err)))
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return ArgsError(fmt.Sprintf("todo: cannot keep the list: %s", syserr.Str(err)))
	}
	// Sync before the rename so a crash cannot leave a half-written
	// list behind; the volume is tiny, so the cost is noise.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return ArgsError(fmt.Sprintf("todo: cannot keep the list: %s", syserr.Str(err)))
	}
	if err := tmp.Close(); err != nil {
		return ArgsError(fmt.Sprintf("todo: cannot keep the list: %s", syserr.Str(err)))
	}
	if err := os.Rename(tmpName, todoPath(sessionDir)); err != nil {
		return ArgsError(fmt.Sprintf("todo: cannot keep the list: %s", syserr.Str(err)))
	}
	return nil
}
