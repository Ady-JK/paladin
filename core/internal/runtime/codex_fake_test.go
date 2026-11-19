package runtime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fake Codex is this test binary itself: with PALADIN_FAKE_CODEX set it
// answers the app-server protocol on stdio instead of running tests.

const (
	fakeCodexEnv      = "PALADIN_FAKE_CODEX"
	fakeCodexLogEnv   = "PALADIN_FAKE_CODEX_LOG"
	fakeCodexScenario = "PALADIN_FAKE_CODEX_SCENARIO"
	fakeCodexManaged  = "PALADIN_FAKE_CODEX_MANAGED"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeCodexEnv) == "1" {
		runFakeCodex()
		return
	}
	os.Exit(m.Run())
}

func runFakeCodex() {
	out := bufio.NewWriter(os.Stdout)
	write := func(v any) {
		data, _ := json.Marshal(v)
		out.Write(append(data, '\n'))
		out.Flush()
	}
	logf := func(line string) {
		if path := os.Getenv(fakeCodexLogEnv); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				f.WriteString(line + "\n")
				f.Close()
			}
		}
	}
	scenario := os.Getenv(fakeCodexScenario)
	note := func(method string, params map[string]any) {
		write(map[string]any{"method": method, "params": params})
	}
	item := func(thread, turn string, it map[string]any, method string) {
		note(method, map[string]any{"threadId": thread, "turnId": turn, "item": it})
	}
	finish := func(thread, turn, status string) {
		note("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": status, "items": []any{}}})
	}
	runTurn := func(thread, turn string) {
		switch scenario {
		case "hang":
			item(thread, turn, map[string]any{"type": "commandExecution", "id": "cmd-1", "command": "sleep 100", "cwd": "/w", "status": "inProgress", "processId": "p-1"}, "item/started")
			return
		case "plan":
			note("turn/plan/updated", map[string]any{"threadId": thread, "turnId": turn, "explanation": "x", "plan": []any{
				map[string]any{"step": "Read the code", "status": "completed"},
				map[string]any{"step": "Write the change", "status": "inProgress"},
				map[string]any{"step": "Run tests", "status": "pending"}}})
			note("turn/plan/updated", map[string]any{"threadId": thread, "turnId": turn, "plan": []any{
				map[string]any{"step": "Read the code", "status": "completed"},
				map[string]any{"step": "Write the change", "status": "completed"},
				map[string]any{"step": "Run tests", "status": "inProgress"}}})
			note("item/plan/delta", map[string]any{"threadId": thread, "turnId": turn, "itemId": "pl-1", "delta": "1. Read\n"})
			note("item/plan/delta", map[string]any{"threadId": thread, "turnId": turn, "itemId": "pl-1", "delta": "2. Write\n"})
			item(thread, turn, map[string]any{"type": "plan", "id": "pl-1", "text": "1. Read\n2. Write\n"}, "item/completed")
			finish(thread, turn, "completed")
			return
		case "ask":
			write(map[string]any{"id": 901, "method": "item/tool/requestUserInput", "params": map[string]any{
				"threadId": thread, "turnId": turn, "itemId": "q-1", "questions": []any{
					map[string]any{"id": "name", "header": "Name", "question": "What should it be called?", "isOther": true,
						"options": []any{map[string]any{"label": "hello", "description": "d"}, map[string]any{"label": "hello_world", "description": "d"}}},
					map[string]any{"id": "where", "header": "Where", "question": "Which file?"}}}})
			return
		case "async-question", "async-question-hold":
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "m0-" + turn, "text": "On it."}, "item/completed")
			question := map[string]any{"type": "agentMessage", "id": "call_" + turn, "delivery": "async", "text": "Pick\n- A\n- B\n\nWhy?",
				"questions": []any{map[string]any{"title": "Pick", "options": []any{"A", "B"}}, map[string]any{"title": "Why?", "options": nil}}}
			item(thread, turn, question, "item/started")
			item(thread, turn, question, "item/completed")
			if scenario == "async-question-hold" {
				return // the turn ends when the answer is steered in
			}
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "m1-" + turn, "text": "Carrying on."}, "item/completed")
			finish(thread, turn, "completed")
			return
		case "background", "background-late":
			cmd := func(turnID, status string) map[string]any {
				c := map[string]any{"type": "commandExecution", "id": "exec-bg", "command": "/bin/zsh -lc 'sleep 25 && echo bgdone'", "cwd": "/w",
					"status": status, "processId": "p-9", "source": "unifiedExecStartup",
					"commandActions": []any{map[string]any{"type": "unknown", "command": "sleep 25 && echo bgdone"}}}
				if status == "completed" {
					c["exitCode"], c["aggregatedOutput"] = 0, "bgdone\n"
				}
				return c
			}
			if scenario == "background-late" && turn == "tu-2" {
				// The first turn's command ends while this one runs.
				note("item/commandExecution/outputDelta", map[string]any{"threadId": thread, "turnId": "tu-1", "itemId": "exec-bg", "delta": "bgdone\n"})
				item(thread, "tu-1", cmd("tu-1", "completed"), "item/completed")
				item(thread, turn, map[string]any{"type": "agentMessage", "id": "m2", "text": "second turn"}, "item/completed")
				finish(thread, turn, "completed")
				return
			}
			item(thread, turn, cmd(turn, "inProgress"), "item/started")
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "m1", "text": "started it"}, "item/completed")
			finish(thread, turn, "completed")
			if scenario == "background" {
				time.Sleep(400 * time.Millisecond)
				note("item/commandExecution/outputDelta", map[string]any{"threadId": thread, "turnId": turn, "itemId": "exec-bg", "delta": "bgdone\n"})
				item(thread, turn, cmd(turn, "completed"), "item/completed")
			}
			return
		case "collab":
			call := func(status string) map[string]any {
				return map[string]any{"type": "collabAgentToolCall", "id": "co-1", "tool": "spawnAgent", "status": status, "senderThreadId": thread,
					"receiverThreadIds": []any{"child-1"}, "prompt": "Look at the parser", "model": "fake-luna", "reasoningEffort": "low",
					"agentsStates": map[string]any{"child-1": map[string]any{"status": "completed", "message": "found 2 bugs"}}}
			}
			item(thread, turn, call("inProgress"), "item/started")
			item(thread, turn, call("completed"), "item/completed")
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "m1", "text": "done"}, "item/completed")
			finish(thread, turn, "completed")
			return
		case "notices":
			note("warning", map[string]any{"threadId": thread, "message": "Codex is running low on context"})
			note("model/rerouted", map[string]any{"threadId": thread, "turnId": turn, "fromModel": "gpt-a", "toModel": "gpt-b", "reason": "highRiskCyberActivity"})
			note("mcpServer/startupStatus/updated", map[string]any{"threadId": thread, "name": "docs", "status": "failed", "error": "spawn failed"})
			note("mcpServer/startupStatus/updated", map[string]any{"threadId": thread, "name": "fine", "status": "ready"})
			note("hook/completed", map[string]any{"threadId": thread, "turnId": turn, "run": map[string]any{"eventName": "stop", "status": "stopped",
				"entries": []any{map[string]any{"kind": "stop", "text": "tests are failing"}, map[string]any{"kind": "context", "text": "ignored"}}}})
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "m1", "text": "done"}, "item/completed")
			finish(thread, turn, "completed")
			return
		case "elicit", "elicit-url":
			params := map[string]any{"threadId": thread, "turnId": turn, "serverName": "codex_apps", "message": "Allow ChatGPT to use Safari?",
				"mode": "form", "_meta": map[string]any{"persist": []any{"session", "always"}},
				"requestedSchema": map[string]any{"type": "object", "required": []any{"approval"}, "properties": map[string]any{
					"approval": map[string]any{"type": "string", "title": "Approval", "oneOf": []any{
						map[string]any{"const": "accept", "title": "Allow"}, map[string]any{"const": "decline", "title": "Do not allow"}}},
					"count": map[string]any{"type": "integer", "title": "How many"},
					"note":  map[string]any{"type": "string", "title": "Note"},
					"tags":  map[string]any{"type": "array", "title": "Tags", "items": map[string]any{"enum": []any{"a", "b"}}},
				}}}
			if scenario == "elicit-url" {
				params = map[string]any{"threadId": thread, "turnId": turn, "serverName": "codex_apps", "message": "Sign in", "mode": "url",
					"url": "https://example.com/auth", "elicitationId": "e1"}
			}
			write(map[string]any{"id": 902, "method": "mcpServer/elicitation/request", "params": params})
			return
		case "patch":
			changes := []any{
				map[string]any{"path": "/w/new.txt", "kind": map[string]any{"type": "add"}, "diff": "hello\nworld\n"},
				map[string]any{"path": "/w/old.txt", "kind": map[string]any{"type": "update"}, "diff": "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n@@ -9,2 +9,2 @@\n-x\n+y\n z\n"},
			}
			item(thread, turn, map[string]any{"type": "fileChange", "id": "fc-1", "status": "inProgress", "changes": changes}, "item/started")
			item(thread, turn, map[string]any{"type": "fileChange", "id": "fc-1", "status": "completed", "changes": changes}, "item/completed")
			finish(thread, turn, "completed")
			return
		case "usage":
			usage := func(total, last map[string]any) {
				note("thread/tokenUsage/updated", map[string]any{"threadId": thread, "turnId": turn, "tokenUsage": map[string]any{
					"total": total, "last": last, "modelContextWindow": 258400}})
			}
			first := map[string]any{"inputTokens": 1000, "cachedInputTokens": 0, "outputTokens": 20, "reasoningOutputTokens": 0, "totalTokens": 1020}
			usage(first, first)
			usage(first, first) // Codex repeats a notice when nothing new was billed
			usage(map[string]any{"inputTokens": 2600, "cachedInputTokens": 0, "outputTokens": 60, "reasoningOutputTokens": 0, "totalTokens": 2660},
				map[string]any{"inputTokens": 1600, "cachedInputTokens": 0, "outputTokens": 40, "reasoningOutputTokens": 0, "totalTokens": 1640})
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "m1", "text": "ok"}, "item/completed")
			finish(thread, turn, "completed")
			return
		case "fail":
			note("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": "failed", "error": map[string]any{"message": "model exploded"}, "items": []any{}}})
			return
		case "approval":
			item(thread, turn, map[string]any{"type": "commandExecution", "id": "cmd-1", "command": "/bin/zsh -lc 'touch x'", "cwd": "/w", "status": "inProgress",
				"commandActions": []any{map[string]any{"type": "unknown", "command": "touch x"}}}, "item/started")
			write(map[string]any{"id": 900, "method": "item/commandExecution/requestApproval", "params": map[string]any{
				"threadId": thread, "turnId": turn, "itemId": "cmd-1", "command": "/bin/zsh -lc 'touch x'", "reason": "may I touch x?",
				"commandActions": []any{map[string]any{"type": "unknown", "command": "touch x"}}}})
			return // the rest runs when the answer arrives
		}
		note("item/reasoning/summaryTextDelta", map[string]any{"threadId": thread, "turnId": turn, "itemId": "r1", "summaryIndex": 0, "delta": "thinking"})
		item(thread, turn, map[string]any{"type": "agentMessage", "id": "m1", "text": ""}, "item/started")
		note("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": turn, "itemId": "m1", "delta": "Looking"})
		note("item/agentMessage/delta", map[string]any{"threadId": thread, "turnId": turn, "itemId": "m1", "delta": " now."})
		item(thread, turn, map[string]any{"type": "agentMessage", "id": "m1", "text": "Looking now."}, "item/completed")
		item(thread, turn, map[string]any{"type": "commandExecution", "id": "cmd-1", "command": "ls", "cwd": "/w", "status": "inProgress"}, "item/started")
		item(thread, turn, map[string]any{"type": "commandExecution", "id": "cmd-1", "command": "ls", "cwd": "/w", "status": "completed",
			"aggregatedOutput": "a.txt\n", "exitCode": 0}, "item/completed")
		note("thread/tokenUsage/updated", map[string]any{"threadId": thread, "turnId": turn, "tokenUsage": map[string]any{
			"last": map[string]any{"inputTokens": 100, "cachedInputTokens": 10, "outputTokens": 7, "reasoningOutputTokens": 2, "totalTokens": 107}}})
		item(thread, turn, map[string]any{"type": "agentMessage", "id": "m2", "text": "Done."}, "item/completed")
		finish(thread, turn, "completed")
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	var thread, turn string
	threads, turnSeq := 0, 0
	var goal map[string]any
	for sc.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result map[string]any  `json:"result"`
			Error  map[string]any  `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" && m.Error != nil { // the client refused a request: wait for the interrupt
			logf("request refused")
			continue
		}
		if m.Method == "" && string(m.ID) == "902" { // an answer to the MCP elicitation
			body, _ := json.Marshal(m.Result)
			logf("elicitation=" + string(body))
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "el", "text": "done"}, "item/completed")
			finish(thread, turn, "completed")
			continue
		}
		if m.Method == "" && string(m.ID) == "901" { // an answer to the questions
			answers, _ := json.Marshal(m.Result["answers"])
			logf("answers=" + string(answers))
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "qa", "text": "thanks"}, "item/completed")
			finish(thread, turn, "completed")
			continue
		}
		if m.Method == "" { // an answer to the approval request
			decision, _ := m.Result["decision"].(string)
			logf("approval decision=" + decision)
			status := "completed"
			if decision == "decline" {
				status = "declined"
			}
			item(thread, turn, map[string]any{"type": "commandExecution", "id": "cmd-1", "command": "touch x", "cwd": "/w", "status": status, "exitCode": 0, "aggregatedOutput": ""}, "item/completed")
			item(thread, turn, map[string]any{"type": "agentMessage", "id": "m2", "text": "after " + decision}, "item/completed")
			finish(thread, turn, "completed")
			continue
		}
		reply := func(result any) { write(map[string]any{"id": m.ID, "result": result}) }
		switch m.Method {
		case "initialize":
			reply(map[string]any{"userAgent": "fake/1.0", "codexHome": "/tmp"})
			if os.Getenv("PALADIN_FAKE_CODEX_CONFIGWARN") == "1" {
				note("configWarning", map[string]any{"summary": "Invalid config.toml", "details": "unknown key tools.bad", "path": "/h/.codex/config.toml"})
				note("deprecationNotice", map[string]any{"summary": "model_reasoning is deprecated"})
			}
		case "configRequirements/read":
			if os.Getenv(fakeCodexManaged) == "1" {
				reply(map[string]any{"requirements": map[string]any{
					"allowedSandboxModes": []any{"read-only", "workspace-write"}, "allowedApprovalPolicies": []any{"on-request", "untrusted"}}})
			} else {
				reply(map[string]any{"requirements": map[string]any{"allowedSandboxModes": nil, "allowedApprovalPolicies": nil}})
			}
		case "model/list":
			reply(map[string]any{"data": []any{
				map[string]any{"id": "fake-luna", "model": "fake-luna", "displayName": "Fake Luna", "isDefault": true, "defaultReasoningEffort": "medium",
					"supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low"}, map[string]any{"reasoningEffort": "medium"}},
					"serviceTiers":              []any{map[string]any{"id": "priority", "name": "Fast", "description": "1.5x speed, increased usage"}}},
				map[string]any{"id": "hidden", "model": "hidden", "hidden": true},
			}})
		case "account/read":
			reply(map[string]any{"account": map[string]any{"type": "chatgpt", "email": "me@example.com"}, "requiresOpenaiAuth": true})
		case "thread/start":
			threads++
			thread = fmt.Sprintf("th-%d", threads)
			logf("thread/start cwd=" + str(m.Params["cwd"]) + " sandbox=" + str(m.Params["sandbox"]) + " approval=" + str(m.Params["approvalPolicy"]))
			reply(map[string]any{"thread": map[string]any{"id": thread}, "model": firstNonEmpty(str(m.Params["model"]), "fake-default")})
		case "thread/resume":
			thread = str(m.Params["threadId"])
			logf("thread/resume " + thread)
			reply(map[string]any{})
		case "turn/start":
			turnSeq++
			turn = fmt.Sprintf("tu-%d", turnSeq)
			thread = str(m.Params["threadId"])
			input, _ := m.Params["input"].([]any)
			first, _ := input[0].(map[string]any)
			if cm, ok := m.Params["collaborationMode"].(map[string]any); ok {
				logf("collaboration mode=" + str(cm["mode"]))
			}
			kinds := []string{}
			for _, in := range input {
				im, _ := in.(map[string]any)
				kind := str(im["type"])
				if kind == "skill" {
					kind += ":" + str(im["name"]) + "@" + str(im["path"])
				}
				kinds = append(kinds, kind)
			}
			logf("turn/start thread=" + str(m.Params["threadId"]) + " model=" + str(m.Params["model"]) + " effort=" + str(m.Params["effort"]) + " text=" + strings.ReplaceAll(str(first["text"]), "\n", "\\n") + " items=" + strings.Join(kinds, ",") + " summary=" + str(m.Params["summary"]) + " tier=" + str(m.Params["serviceTier"]))
			reply(map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress"}})
			go runTurn(thread, turn)
		case "thread/fork":
			logf("thread/fork thread=" + str(m.Params["threadId"]) + " last=" + str(m.Params["lastTurnId"]))
			if scenario == "fork-fails" {
				write(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "session th-1 is busy"}})
				continue
			}
			threads++
			reply(map[string]any{"thread": map[string]any{"id": fmt.Sprintf("th-%d", threads), "forkedFromId": str(m.Params["threadId"])}, "model": "fake-luna"})
		case "account/rateLimits/read":
			soon := time.Now().Add(2 * time.Hour).Unix()
			reply(map[string]any{"rateLimits": map[string]any{"limitId": "codex", "planType": "plus",
				"primary":   map[string]any{"usedPercent": 42, "windowDurationMins": 300, "resetsAt": soon},
				"secondary": map[string]any{"usedPercent": 130, "windowDurationMins": 10080, "resetsAt": soon + 86400}}})
		case "config/read":
			logf("config/read")
			cfg := map[string]any{}
			if v := os.Getenv("PALADIN_FAKE_CODEX_SUMMARY"); v != "" {
				cfg["model_reasoning_summary"] = v
			}
			reply(map[string]any{"config": cfg})
		case "mcpServerStatus/list":
			logf("mcpServerStatus/list thread=" + str(m.Params["threadId"]) + " detail=" + str(m.Params["detail"]))
			reply(map[string]any{"data": []any{
				map[string]any{"name": "codex_apps", "runtimeStatus": "connected", "authStatus": "unsupported", "tools": map[string]any{"a": map[string]any{}, "b": map[string]any{}}},
				map[string]any{"name": "docs", "runtimeStatus": "authenticationRequired", "authStatus": "notLoggedIn", "tools": map[string]any{}},
				map[string]any{"name": "bad", "runtimeStatus": "failed", "authStatus": "unknown", "toolsError": "spawn failed", "tools": map[string]any{}},
				map[string]any{"name": "idle", "runtimeStatus": nil, "authStatus": "bearerToken", "tools": map[string]any{"x": map[string]any{}}},
			}, "nextCursor": nil})
		case "skills/list":
			logf("skills/list cwds=" + fmt.Sprint(m.Params["cwds"]))
			reply(map[string]any{"data": []any{map[string]any{"cwd": "/w", "errors": []any{}, "skills": []any{
				map[string]any{"name": "guava", "description": "Answers with a fixed word.", "path": "/w/.agents/skills/guava/SKILL.md", "scope": "repo", "enabled": true,
					"interface": map[string]any{"displayName": "Guava", "shortDescription": "Fixed word"}},
				map[string]any{"name": "off", "description": "Disabled.", "path": "/w/.agents/skills/off/SKILL.md", "scope": "repo", "enabled": false},
			}}}})
		case "thread/read":
			found := false
			for _, t := range fakeThreads(map[string]any{}) {
				if tm := t.(map[string]any); tm["id"] == str(m.Params["threadId"]) {
					reply(map[string]any{"thread": tm})
					found = true
				}
			}
			if !found {
				write(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "thread not found"}})
			}
		case "thread/list":
			all := fakeThreads(m.Params)
			start, _ := strconv.Atoi(str(m.Params["cursor"]))
			start = min(max(start, 0), len(all))
			end := len(all)
			if n, ok := m.Params["limit"].(float64); ok && n > 0 {
				end = min(end, start+int(n))
			}
			var next any
			if end < len(all) {
				next = strconv.Itoa(end)
			}
			reply(map[string]any{"data": all[start:end], "nextCursor": next})
		case "thread/turns/list":
			turns := fakeThreadTurns(str(m.Params["threadId"]))
			if path := os.Getenv("PALADIN_FAKE_CODEX_HISTORY"); path != "" {
				data, _ := os.ReadFile(path)
				var histories map[string][]any
				if json.Unmarshal(data, &histories) == nil {
					if saved, ok := histories[str(m.Params["threadId"])]; ok {
						turns = saved
					}
				}
			}
			if str(m.Params["sortDirection"]) == "desc" {
				for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
					turns[i], turns[j] = turns[j], turns[i]
				}
			}
			if limit, ok := m.Params["limit"].(float64); ok && limit > 0 && int(limit) < len(turns) {
				turns = turns[:int(limit)]
			}
			reply(map[string]any{"data": turns, "nextCursor": nil})
		case "turn/steer":
			input, _ := m.Params["input"].([]any)
			text := ""
			if len(input) > 0 {
				text = str(input[0].(map[string]any)["text"])
			}
			logf("turn/steer text=" + strings.ReplaceAll(text, "\n", "\\n"))
			reply(map[string]any{"turnId": turn})
			if scenario == "async-question-hold" {
				go func(thread, turn string) {
					item(thread, turn, map[string]any{"type": "agentMessage", "id": "got", "text": "got your answer"}, "item/completed")
					finish(thread, turn, "completed")
				}(thread, turn)
			}
		case "thread/backgroundTerminals/terminate":
			logf("terminal terminate thread=" + str(m.Params["threadId"]) + " process=" + str(m.Params["processId"]))
			reply(map[string]any{"terminated": true})
		case "thread/revert":
			logf("thread/revert thread=" + str(m.Params["threadId"]) + " before=" + str(m.Params["beforeTurnId"]))
			if scenario == "revert-fails" {
				write(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "thread uses legacy history and cannot be reverted"}})
				continue
			}
			reply(map[string]any{"thread": map[string]any{"id": str(m.Params["threadId"])}})
		case "thread/compact/start":
			logf("thread/compact/start")
			reply(map[string]any{})
			go func(thread string) {
				note("turn/started", map[string]any{"threadId": thread, "turn": map[string]any{"id": "tu-c", "status": "inProgress"}})
				item(thread, "tu-c", map[string]any{"type": "contextCompaction", "id": "cc-1"}, "item/started")
				item(thread, "tu-c", map[string]any{"type": "contextCompaction", "id": "cc-1"}, "item/completed")
				finish(thread, "tu-c", "completed")
			}(str(m.Params["threadId"]))
		case "review/start":
			target, _ := json.Marshal(m.Params["target"])
			logf("review/start " + string(target))
			reply(map[string]any{"reviewThreadId": "rv", "turn": map[string]any{"id": "tu-r"}})
			go func(thread string) {
				note("turn/started", map[string]any{"threadId": thread, "turn": map[string]any{"id": "tu-r", "status": "inProgress"}})
				item(thread, "tu-r", map[string]any{"type": "agentMessage", "id": "rm", "text": "Looks fine."}, "item/completed")
				finish(thread, "tu-r", "completed")
			}(str(m.Params["threadId"]))
		case "thread/goal/set":
			logf("goal/set objective=" + str(m.Params["objective"]) + " status=" + str(m.Params["status"]))
			goal = map[string]any{"objective": firstNonEmpty(str(m.Params["objective"]), str(goal["objective"])), "status": firstNonEmpty(str(m.Params["status"]), "active"), "tokensUsed": 5, "timeUsedSeconds": 3}
			reply(map[string]any{"goal": goal})
			if str(m.Params["objective"]) != "" {
				// Codex works on a goal by itself: a turn nobody asked for.
				go func(thread string) {
					note("turn/started", map[string]any{"threadId": thread, "turn": map[string]any{"id": "tu-g", "status": "inProgress"}})
					item(thread, "tu-g", map[string]any{"type": "agentMessage", "id": "gm", "text": "Working on the goal."}, "item/completed")
					finish(thread, "tu-g", "completed")
				}(str(m.Params["threadId"]))
			}
		case "thread/goal/get":
			reply(map[string]any{"goal": goal})
		case "thread/goal/clear":
			goal = nil
			reply(map[string]any{"cleared": true})
		case "feedback/upload":
			logf("feedback reason=" + str(m.Params["reason"]))
			reply(map[string]any{"threadId": "fb-123"})
		case "turn/interrupt":
			logf("turn/interrupt")
			reply(map[string]any{})
			finish(thread, turn, "interrupted")
		default:
			reply(map[string]any{})
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// codexRuntime is a runtime wired to the fake Codex.
func codexRuntime(t *testing.T, scenario string) (*AgentRuntime, *eventLog, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := t.TempDir() + "/fake.log"
	t.Setenv("PALADIN_CODEX_BIN", exe)
	t.Setenv(fakeCodexEnv, "1")
	t.Setenv(fakeCodexScenario, scenario)
	t.Setenv(fakeCodexLogEnv, logPath)
	rt, _ := testRuntime(t)
	log := newEventLog()
	rt.SetTurnSink(log.sink)
	t.Cleanup(func() { rt.ShutdownTurns(3 * time.Second) })
	return rt, log, logPath
}

func fakeLog(t *testing.T, path string) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	return string(data)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// fakeThreads are the threads the fake Codex has on disk, from other clients.
func fakeThreads(params map[string]any) []any {
	if path := os.Getenv("PALADIN_FAKE_CODEX_THREADS"); path != "" {
		var custom []any
		data, _ := os.ReadFile(path)
		if json.Unmarshal(data, &custom) == nil {
			return custom
		}
	}
	all := []map[string]any{
		{"id": "th-old", "preview": "Fix the bug", "name": nil, "cwd": os.Getenv("PALADIN_FAKE_CODEX_CWD"), "model": "fake-luna", "updatedAt": 1794600000, "createdAt": 1794590000, "source": "cli", "status": map[string]any{"type": "notLoaded"}},
		{"id": "th-async", "preview": "Ask me something", "name": nil, "cwd": os.Getenv("PALADIN_FAKE_CODEX_CWD"), "model": "fake-luna", "updatedAt": 1794610000, "createdAt": 1794595000, "source": "cli", "status": map[string]any{"type": "notLoaded"}},
		{"id": "th-other", "preview": "Something elsewhere", "name": "Named thread", "cwd": "/elsewhere", "model": "fake-luna", "updatedAt": 1794500000, "createdAt": 1794400000, "source": "vscode", "status": map[string]any{"type": "notLoaded"}},
	}
	var out []any
	for _, t := range all {
		if t["id"] == "th-async" && os.Getenv("PALADIN_FAKE_CODEX_ASYNC") != "1" {
			continue // only the import test asks for it
		}
		if cwd := str(params["cwd"]); cwd != "" && cwd != t["cwd"] {
			continue
		}
		if q := strings.ToLower(str(params["searchTerm"])); q != "" && !strings.Contains(strings.ToLower(str(t["preview"])+str(t["name"])), q) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func fakeThreadTurns(thread string) []any {
	if thread == "th-async" {
		return []any{
			map[string]any{"id": "tu-q1", "status": "completed", "items": []any{
				map[string]any{"type": "userMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": "Ask me something"}}},
				map[string]any{"type": "agentMessage", "id": "call_old", "delivery": "async", "text": "Pick\n- A\n- B", "phase": "final_answer",
					"questions": []any{map[string]any{"title": "Pick", "options": []any{"A", "B"}}}},
				map[string]any{"type": "commandExecution", "id": "exec-bg", "command": "sleep 5", "cwd": "/w", "status": "completed", "processId": "p", "exitCode": 0, "aggregatedOutput": "ok\n"},
				map[string]any{"type": "agentMessage", "id": "m1", "text": "Waiting on you.", "phase": "final_answer"},
			}},
			map[string]any{"id": "tu-q2", "status": "completed", "items": []any{
				map[string]any{"type": "userMessage", "id": "u2", "content": []any{map[string]any{"type": "text", "text": "<send_user_message_question_reply>\n[{\"answer\":\"B\",\"question\":\"Pick\",\"questionItemId\":\"[\\\"request_user_input_async\\\",\\\"call_old\\\",0]\"}]\n</send_user_message_question_reply>"}}},
				map[string]any{"type": "agentMessage", "id": "m2", "text": "You chose B.", "phase": "final_answer"},
			}},
		}
	}
	if thread == "th-repair" {
		cmd := func(id, status string, exit int, out string) map[string]any {
			c := map[string]any{"type": "commandExecution", "id": id, "command": "sleep 1", "cwd": "/w", "status": status, "processId": "p-" + id}
			if status != "inProgress" {
				c["exitCode"], c["aggregatedOutput"] = exit, out
			}
			return c
		}
		return []any{map[string]any{"id": "tu-r1", "status": "completed", "items": []any{
			map[string]any{"type": "userMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": "run things"}}},
			cmd("exec-old", "completed", 0, "done\n"),
			cmd("exec-int", "failed", 3, "boom\n"),
			cmd("exec-lost", "inProgress", 0, ""),
		}}}
	}
	if thread != "th-old" {
		return []any{}
	}
	changes := []any{map[string]any{"path": "/w/fix.txt", "kind": map[string]any{"type": "add"}, "diff": "fixed\n"}}
	return []any{
		map[string]any{"id": "tu-a1", "status": "completed", "startedAt": 1794590100, "completedAt": 1794590160, "durationMs": 60000, "items": []any{
			map[string]any{"type": "userMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": "Fix the bug"}}},
			map[string]any{"type": "reasoning", "id": "r1", "summary": []any{"Looking at the code"}, "content": []any{}},
			map[string]any{"type": "commandExecution", "id": "c1", "command": "/bin/zsh -lc 'ls'", "cwd": "/w", "status": "completed", "aggregatedOutput": "a.txt\n", "exitCode": 0,
				"commandActions": []any{map[string]any{"type": "unknown", "command": "ls"}}},
			map[string]any{"type": "fileChange", "id": "f1", "status": "completed", "changes": changes},
			map[string]any{"type": "agentMessage", "id": "m1", "text": "Done.", "phase": "final_answer"},
		}},
		map[string]any{"id": "tu-a2", "status": "completed", "items": []any{
			map[string]any{"type": "userMessage", "id": "u2", "content": []any{map[string]any{"type": "text", "text": "Thanks"}}},
			map[string]any{"type": "agentMessage", "id": "m2", "text": "You are welcome.", "phase": "final_answer"},
		}},
	}
}
