package runtime

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/claude"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Background tasks have their own cards because the initiating Bash/Agent
// invocation may finish long before the work it scheduled. Completion can
// arrive between user turns and must update the persisted card then as well.
func (th *claudeThread) taskEvent(raw claude.Raw, run *claudeRun) error {
	if raw.Str("type") != "system" {
		return nil
	}
	subtype := raw.Str("subtype")
	id := raw.Str("task_id")
	if id == "" || (subtype != "task_started" && subtype != "task_progress" && subtype != "task_notification" && subtype != "task_updated") {
		return nil
	}
	callID := "claude-task-" + id
	if subtype == "task_started" {
		args := mustJSON(map[string]any{"task_id": id, "description": raw.Str("description"), "task_type": raw.Str("task_type"), "tool_use_id": raw.Str("tool_use_id")})
		th.mu.Lock()
		if th.tasks == nil {
			th.tasks = map[string]string{}
		}
		th.tasks[id] = args
		th.mu.Unlock()
		if run != nil {
			run.p.Flush([]transcript.ToolCall{{ID: callID, Name: "claude_task", Arguments: args}})
			name := "claude_task"
			running := `{"status":"running"}`
			run.p.Records = append(run.p.Records, transcript.Message{Role: transcript.RoleTool, ToolCallID: &callID, Name: &name, Content: running})
			run.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: callID, Name: name, Arguments: args, Result: running, Status: session.ToolRunning}})
		}
		return nil
	}
	status := raw.Str("status")
	if subtype == "task_updated" {
		status = raw.Obj("patch").Str("status")
	}
	if status != "completed" && status != "failed" && status != "stopped" && status != "cancelled" {
		return nil
	}
	text := raw.Str("summary")
	if path := raw.Str("output_file"); path != "" {
		// Bounded tail of Claude's own task-output file, never the whole log.
		f, err := os.Open(path)
		if err == nil {
			st, e := f.Stat()
			if e == nil {
				b := make([]byte, min(st.Size(), 64*1024))
				n, _ := f.ReadAt(b, max(0, st.Size()-int64(len(b))))
				text = string(b[:n])
			}
			_ = f.Close()
		}
	}
	result := mustJSON(map[string]any{"status": status, "output": text})
	th.mu.Lock()
	args := th.tasks[id]
	delete(th.tasks, id)
	th.mu.Unlock()
	failed := status != "completed"
	if run != nil {
		for i := range run.p.Records {
			m := &run.p.Records[i]
			if m.Role == transcript.RoleTool && m.ToolCallID != nil && *m.ToolCallID == callID {
				m.Content = result
				m.IsError = failed
			}
		}
		toolStatus := session.ToolOK
		if failed {
			toolStatus = session.ToolFailed
		}
		run.send(agent.ChatChunk{Tool: &agent.ToolActivity{ID: callID, Name: "claude_task", Arguments: args, Result: result, Status: toolStatus}})
	}
	if _, err := th.r.patchToolRow(th.sessionID, th.cwd, callID, result, failed); err != nil {
		return fmt.Errorf("settle Claude background task: %w", err)
	}
	th.mu.Lock()
	if th.taskResults == nil {
		th.taskResults = map[string]claude.Raw{}
	}
	th.taskResults[callID] = claude.Decode([]byte(result))
	th.mu.Unlock()
	if run == nil {
		th.r.hub.PublishSession(th.sessionID, "session/refresh")
	}
	return nil
}
func (th *claudeThread) flushTasks() error {
	th.mu.Lock()
	results := th.taskResults
	th.taskResults = map[string]claude.Raw{}
	th.mu.Unlock()
	for id, result := range results {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err := th.r.patchToolRow(th.sessionID, th.cwd, id, string(b), result.Str("status") != "completed"); err != nil {
			return err
		}
	}
	return nil
}
