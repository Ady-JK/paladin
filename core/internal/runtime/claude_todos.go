package runtime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/harness/claudeadapter"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type claudeTodoItem = tools.TodoItem

var claudeTaskNumber = regexp.MustCompile(`(?i)task\s+#(\d+)`)

// todoEvent translates both the older TodoWrite and current TaskCreate/Update
// tools into the existing activity-list panel, retaining native task IDs.
func (th *claudeThread) todoEvent(raw claude.Raw, p *claudeadapter.Projection, dir string) (string, error) {
	if raw.Str("type") != "user" {
		return "", nil
	}
	changed := false
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.todos == nil {
		th.todos = map[string]tools.TodoItem{}
	}
	for _, result := range raw.Obj("message").List("content") {
		if result.Str("type") != "tool_result" || result.Bool("is_error") {
			continue
		}
		id := result.Str("tool_use_id")
		card := p.Tools[id]
		args := claude.Decode([]byte(card.Args))
		name := p.NativeTools[id]
		switch name {
		case "TodoWrite":
			data := []byte(card.Args)
			if err := atomicfile.Write(filepath.Join(dir, session.TodoFileName), data); err != nil {
				return "", err
			}
			return string(data), nil
		case "TaskCreate":
			nativeID := raw.Obj("tool_use_result").Obj("task").Str("id")
			if nativeID == "" {
				if match := claudeTaskNumber.FindStringSubmatch(result.Str("content")); len(match) > 1 {
					nativeID = match[1]
				}
			}
			if nativeID == "" {
				continue
			}
			th.todos[nativeID] = tools.TodoItem{Content: args.Str("subject"), Status: tools.TodoNotStarted}
			changed = true
		case "TaskUpdate":
			nativeID := args.Str("taskId")
			if nativeID == "" && args.Int("taskId") > 0 {
				nativeID = fmt.Sprint(args.Int("taskId"))
			}
			item, ok := th.todos[nativeID]
			if !ok {
				continue
			}
			if subject := args.Str("subject"); subject != "" {
				item.Content = subject
			}
			switch args.Str("status") {
			case "pending":
				item.Status = tools.TodoNotStarted
			case "in_progress":
				item.Status = tools.TodoInProgress
			case "completed":
				item.Status = tools.TodoFinished
			case "deleted":
				item.Status = tools.TodoCancelled
			}
			th.todos[nativeID] = item
			changed = true
		}
	}
	if !changed {
		return "", nil
	}
	keys := make([]string, 0, len(th.todos))
	for id := range th.todos {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	todos := make([]tools.TodoItem, 0, len(keys))
	active := false
	for _, id := range keys {
		item := th.todos[id]
		if item.Status == tools.TodoInProgress {
			if active {
				item.Status = tools.TodoNotStarted
			}
			active = true
		}
		todos = append(todos, item)
	}
	data, err := json.Marshal(map[string]any{"todos": todos})
	if err != nil {
		return "", err
	}
	if err = atomicfile.Write(filepath.Join(dir, session.TodoFileName), data); err != nil {
		return "", err
	}
	native, err := json.Marshal(th.todos)
	if err != nil {
		return "", err
	}
	if err = atomicfile.Write(filepath.Join(dir, "claude-tasks.json"), native); err != nil {
		return "", err
	}
	return string(data), nil
}
