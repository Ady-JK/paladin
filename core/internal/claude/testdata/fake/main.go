package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/claude"
)

func emit(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
func ack(id string, v any) {
	emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": v}})
}
func main() {
	if len(os.Args) > 2 && os.Args[1] == "auth" {
		emit(map[string]any{"loggedIn": true, "authMethod": "claude.ai"})
		return
	}
	if path := os.Getenv("PALADIN_CLAUDE_TEST_ARGS"); path != "" {
		b, _ := json.Marshal(os.Args[1:])
		_ = atomicfile.Write(path, b)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 32*1024*1024)
	number := 0
	pending := ""
	toolID := ""
	promptID := ""
	finish := func(text string, failed bool) {
		emit(map[string]any{"type": "assistant", "uuid": fmt.Sprint("a", number), "parent_tool_use_id": nil, "message": map[string]any{"id": fmt.Sprint("m", number), "model": "claude-haiku-5-5", "content": []any{map[string]any{"type": "text", "text": text}}}})
		emit(map[string]any{"type": "result", "subtype": "success", "is_error": failed, "result": text, "session_id": "fake", "usage": map[string]any{"input_tokens": 2, "output_tokens": 3}, "total_cost_usd": float64(number) * 0.001, "modelUsage": map[string]any{"claude-haiku-5-5": map[string]any{"contextWindow": 200000}}})
	}
	for scanner.Scan() {
		r := claude.Decode(scanner.Bytes())
		switch r.Str("type") {
		case "control_request":
			req := r.Obj("request")
			switch req.Str("subtype") {
			case "initialize":
				ack(r.Str("request_id"), map[string]any{"claude_code_version": "2.1.326", "hook_on_failure_supported": true, "models": []any{map[string]any{"value": "default", "resolvedModel": "claude-haiku-5-5", "displayName": "Haiku 5.5", "supportedEffortLevels": []string{"low", "high"}}}, "commands": []any{map[string]any{"name": "review", "description": "Review code", "argumentHint": "[target]"}}})
				if os.Getenv("PALADIN_CLAUDE_TEST_SCENARIO") == "malformed" {
					fmt.Println("{invalid")
					time.Sleep(time.Minute)
				}
				if os.Getenv("PALADIN_CLAUDE_TEST_SCENARIO") == "blocked" {
					time.Sleep(time.Minute)
				}
			case "interrupt":
				ack(r.Str("request_id"), map[string]any{})
				finish("interrupted", true)
			default:
				ack(r.Str("request_id"), map[string]any{})
			}
		case "user":
			number++
			promptID = r.Str("uuid")
			emit(r)
			text := claude.UserText(r.Obj("message"))
			if strings.Contains(text, "permission-test") {
				toolID = fmt.Sprint("tool", number)
				pending = "hook"
				emit(map[string]any{"type": "assistant", "uuid": fmt.Sprint("toolmsg", number), "message": map[string]any{"id": fmt.Sprint("tm", number), "content": []any{map[string]any{"type": "tool_use", "id": toolID, "name": "Write", "input": map[string]any{"file_path": "test.txt", "content": "approved"}}}}})
				emit(map[string]any{"type": "control_request", "request_id": "hook", "request": map[string]any{"subtype": "hook_callback", "callback_id": "paladin-permission", "tool_use_id": toolID, "input": map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "test.txt", "content": "approved"}}}})
			} else if strings.Contains(text, "ask-test") {
				pending = "question"
				emit(map[string]any{"type": "control_request", "request_id": "question", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "AskUserQuestion", "tool_use_id": "ask-1", "input": map[string]any{"questions": []any{map[string]any{"question": "Choose a format", "header": "Format", "options": []any{map[string]any{"label": "First"}, map[string]any{"label": "Second"}}, "multiSelect": false}}}}})
			} else if strings.Contains(text, "long-test") {
				emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "waiting"}}})
				// Wait for an interrupt or another prompt.
			} else {
				mid := fmt.Sprint("m", number)
				emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start", "message": map[string]any{"id": mid}}})
				emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "hello "}}})
				finish("hello world", false)
			}
		case "control_response":
			response := r.Obj("response").Obj("response")
			if pending == "question" {
				pending = ""
				finish(response.Obj("updatedInput").Obj("answers").Str("Choose a format"), false)
				continue
			}
			if pending == "hook" {
				if response.Obj("hookSpecificOutput").Str("permissionDecision") == "deny" {
					pending = ""
					finish("denied", false)
					continue
				}
				pending = "permission"
				emit(map[string]any{"type": "control_request", "request_id": "permission", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Write", "tool_use_id": toolID, "input": map[string]any{"file_path": "test.txt", "content": "approved"}}})
			} else if pending == "permission" {
				pending = ""
				result := "denied"
				if response.Str("behavior") == "allow" {
					cwd, _ := os.Getwd()
					_ = atomicfile.Write(filepath.Join(cwd, "test.txt"), []byte("approved"))
					result = "written"
				}
				emit(map[string]any{"type": "user", "uuid": promptID + "-result", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolID, "content": result, "is_error": result == "denied"}}}})
				finish(result, false)
			}
		}
	}
}
