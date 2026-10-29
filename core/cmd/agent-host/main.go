// agent-host is the stdio sidecar the Electron main process spawns.
// Protocol is JSONL: one JSON object per line.
//
// Electron -> Go (stdin):
//
//	{"id":1,"method":"list_providers","params":{}}
//	{"id":2,"method":"chat","params":{
//	  "sessionId":"","message":"hi","cwd":"/tmp/proj",
//	  "provider":null,"model":null,"systemPrompt":null,"resume":false}}
//
// Go -> Electron (stdout):
//
//	unary:  {"id":1,"ok":true,"result":[...]} | {"id":1,"ok":false,"error":"..."}
//	stream: {"event":"chat/delta","streamId":2,"sessionId":"...","delta":"..."}
//	        {"event":"chat/tool","streamId":2,"sessionId":"...","tool":{...}}
//	        {"id":2,"ok":true,"result":{"sessionId":"...","reply":"...",
//	         "stopped":false,"needsContinue":false}}
//
// Logs always go to stderr so stdout stays pure JSONL.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/procstat"
	"github.com/Ady-JK/paladin/core/internal/runtime"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type Request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	ID     json.RawMessage `json:"id"`
	OK     bool            `json:"ok"`
	Result any             `json:"result,omitempty"`
	Error  *string         `json:"error,omitempty"`
}

type ChatParams struct {
	SessionID    string  `json:"sessionId"`
	Message      string  `json:"message"`
	Cwd          string  `json:"cwd"`
	Provider     *string `json:"provider"`
	Model        *string `json:"model"`
	SystemPrompt *string `json:"systemPrompt"`
	Resume       bool    `json:"resume"`
	Skill        *string `json:"skill"`
	// Display is how the client rendered the message; echoed in turn
	// info so a re-attaching UI can redraw the bubble.
	Display string `json:"display"`
}

func (p ChatParams) request() agent.Request {
	return agent.Request{
		SessionID: p.SessionID, Message: p.Message, Cwd: p.Cwd,
		Provider: p.Provider, Model: p.Model,
		SystemPrompt: p.SystemPrompt, Resume: p.Resume,
		Skill: p.Skill,
	}
}

type StrField struct {
	Name string  `json:"name"`
	Path string  `json:"path"`
	Cwd  *string `json:"cwd"`
}

var (
	stdoutMu sync.Mutex
	logger   *logWriter
)

type logWriter struct {
	mu   sync.Mutex
	file *os.File
}

func (l *logWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("%s %s", time.Now().UTC().Format(time.RFC3339), p)
	if _, err := os.Stderr.WriteString(line); err != nil {
		return 0, err
	}
	if l.file != nil {
		if _, err := l.file.WriteString(line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func logf(format string, args ...any) {
	if logger == nil {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		return
	}
	fmt.Fprintf(logger, format+"\n", args...)
}

func emit(v any) {
	data := jsonx.MustMarshal(v)
	data = append(data, '\n')
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	_, _ = os.Stdout.Write(data)
}

func okResponse(id json.RawMessage, result any) Response {
	return Response{ID: id, OK: true, Result: result}
}

func errResponse(id json.RawMessage, msg string) Response {
	if msg == "" {
		msg = "unknown error"
	}
	return Response{ID: id, OK: false, Error: &msg}
}

func main() {
	dataDir := config.DataDir()
	initLogging(dataDir)
	rt := runtime.New(dataDir, config.NewConfig(dataDir))
	installTurnSink(rt)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go handleSignals(rt, sigs, os.Exit)

	fmt.Fprintf(os.Stderr, "agent-host ready dir=%s\n", rt.GetWorkspace())

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	var wg sync.WaitGroup
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := line
		if len(trimmed) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
			logf("WARN bad request line: %v", err)
			continue
		}
		if req.Method == "chat" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				runChat(rt, req)
			}()
		} else {
			var resp Response
			if err := guard("request "+req.Method, func() { resp = runUnary(rt, &req) }); err != nil {
				resp = errResponse(req.ID, err.Error())
			}
			emit(resp)
		}
	}
	if err := scanner.Err(); err != nil {
		logf("WARN read stdin: %v", err)
	}
	// The client is gone: stop every turn, let them persist, then reap
	// blocking chat requests so their final lines flush.
	rt.ShutdownTurns(20 * time.Second)
	wg.Wait()
}

// handleSignals treats SIGTERM/SIGINT like the client hanging up: stop
// every turn so it persists what it did, then exit cleanly.
func handleSignals(rt *runtime.AgentRuntime, sigs <-chan os.Signal, exit func(int)) {
	sig, ok := <-sigs
	if !ok {
		return
	}
	logf("agent-host: %v, stopping turns", sig)
	rt.ShutdownTurns(10 * time.Second)
	exit(0)
}

func initLogging(dataDir string) {
	logsDir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(logsDir, 0o755)
	f, err := os.OpenFile(filepath.Join(logsDir, "paladin.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f = nil
	}
	logger = &logWriter{file: f}
}

func strFieldParams(raw json.RawMessage) StrField {
	var p StrField
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	return p
}

func stringParam(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	var s string
	if v, ok := m[key]; ok {
		_ = json.Unmarshal(v, &s)
	}
	return s
}

func intParam(raw json.RawMessage, key string, def int64) int64 {
	if len(raw) == 0 {
		return def
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return def
	}
	if v, ok := m[key]; ok {
		var n float64
		if err := json.Unmarshal(v, &n); err == nil {
			return int64(n)
		}
	}
	return def
}

func runUnary(rt *runtime.AgentRuntime, req *Request) Response {
	ok := func(result any) Response { return okResponse(req.ID, result) }
	failed := func(err error) Response {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		return errResponse(req.ID, msg)
	}
	switch req.Method {
	case "ping":
		return ok(map[string]bool{"ok": true})
	case "list_providers":
		return ok(rt.ListProviders())
	case "refresh_providers":
		// {name} refreshes one provider synchronously; no name starts
		// the background refresh-all. Both are user-initiated only.
		if name := strings.TrimSpace(strFieldParams(req.Params).Name); name != "" {
			res, err := rt.RefreshProvider(name)
			if err != nil {
				return failed(err)
			}
			return ok(res)
		}
		return ok(rt.RefreshProviders())
	case "set_default_provider":
		p := strFieldParams(req.Params)
		infos, err := rt.SetDefaultProvider(p.Name)
		if err != nil {
			return failed(err)
		}
		return ok(infos)
	case "add_provider":
		var input config.ProviderInput
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &input)
		}
		info, err := rt.AddProvider(input)
		if err != nil {
			return failed(err)
		}
		return ok(info)
	case "update_provider":
		var input config.ProviderInput
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &input)
		}
		info, err := rt.UpdateProvider(input)
		if err != nil {
			return failed(err)
		}
		return ok(info)
	case "get_workspace":
		return ok(rt.GetWorkspace())
	case "set_workspace":
		p := strFieldParams(req.Params)
		v, err := rt.SetWorkspace(p.Path)
		if err != nil {
			return failed(err)
		}
		return ok(v)
	case "resolve_workspace":
		p := strFieldParams(req.Params)
		cwd := ""
		if p.Cwd != nil {
			cwd = *p.Cwd
		}
		if strings.TrimSpace(cwd) != "" {
			return ok(strings.TrimSpace(cwd))
		}
		return ok(rt.FsHome())
	case "fs_home":
		return ok(rt.FsHome())
	case "fs_list":
		list, err := rt.FsList(stringParam(req.Params, "path"))
		if err != nil {
			return failed(err)
		}
		return ok(list)
	case "get_prompt":
		return ok(rt.GetPrompt())
	case "set_prompt":
		updated, err := rt.SetPrompt(stringParam(req.Params, "systemPrompt"))
		if err != nil {
			return failed(err)
		}
		return ok(updated)
	case "reset_prompt":
		updated, err := rt.ResetPrompt()
		if err != nil {
			return failed(err)
		}
		return ok(updated)
	case "file_complete":
		entries, err := rt.FileComplete(stringParam(req.Params, "query"), stringParam(req.Params, "cwd"), int(intParam(req.Params, "limit", 20)))
		if err != nil {
			return failed(err)
		}
		return ok(entries)
	case "list_skills":
		return ok(rt.ListSkills(stringParam(req.Params, "cwd")))
	case "open_session":
		sess, err := rt.OpenSession(stringParam(req.Params, "sessionId"), stringParam(req.Params, "cwd"))
		if err != nil {
			return failed(err)
		}
		return ok(sess)
	case "list_sessions":
		infos, err := rt.ListSessions(stringParam(req.Params, "cwd"), int(intParam(req.Params, "limit", 100)))
		if err != nil {
			return failed(err)
		}
		return ok(infos)
	case "session_history":
		history, err := rt.SessionHistory(stringParam(req.Params, "sessionId"), stringParam(req.Params, "cwd"))
		if err != nil {
			return failed(err)
		}
		return ok(history)
	case "session_todos":
		todos, err := rt.SessionTodos(stringParam(req.Params, "sessionId"), stringParam(req.Params, "cwd"))
		if err != nil {
			return failed(err)
		}
		return ok(todos)
	case "session_usage":
		usage, err := rt.SessionUsage(stringParam(req.Params, "sessionId"), stringParam(req.Params, "cwd"))
		if err != nil {
			return failed(err)
		}
		return ok(usage)
	case "overall_usage":
		{
			limit := int(intParam(req.Params, "limit", 50))
			usage, err := rt.OverallUsage(stringParam(req.Params, "cwd"), limit)
			if err != nil {
				return failed(err)
			}
			return ok(usage)
		}
	case "delete_session":
		v, err := rt.DeleteSession(stringParam(req.Params, "sessionId"), stringParam(req.Params, "cwd"))
		if err != nil {
			return failed(err)
		}
		return ok(v)
	case "submit_chat":
		installTurnSink(rt)
		p, err := chatParams(req.Params)
		if err != nil {
			return failed(err)
		}
		info, _, err := rt.SubmitTurn(p.request(), runtime.SubmitOpts{Display: p.Display})
		if err != nil {
			return failed(err)
		}
		return ok(info)
	case "turns":
		return ok(rt.Turns())
	case "resources":
		// CPU/RSS of the core plus each session's shell process trees.
		return ok(procstat.Default.Snapshot())
	case "attach":
		att, err := rt.Attach(stringParam(req.Params, "sessionId"), stringParam(req.Params, "cwd"))
		if err != nil {
			return failed(err)
		}
		return ok(att)
	case "cancel_turn":
		return ok(rt.CancelTurn(stringParam(req.Params, "turnId")))
	case "stop_chat":
		return ok(rt.StopIn(stringParam(req.Params, "sessionId"), stringParam(req.Params, "cwd")))
	case "answer_ask":
		var p struct {
			SessionID string            `json:"sessionId"`
			CallID    string            `json:"callId"`
			Answers   []tools.AskAnswer `json:"answers"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		v, err := rt.AnswerAsk(p.SessionID, p.CallID, p.Answers)
		if err != nil {
			return failed(err)
		}
		return ok(v)
	case "cancel_ask":
		return ok(rt.CancelAsk(stringParam(req.Params, "sessionId"), stringParam(req.Params, "callId")))
	default:
		return errResponse(req.ID, fmt.Sprintf("unknown method %q", req.Method))
	}
}

// installTurnSink streams every hub event (chat/* and turn/*) to the
// client. Idempotent: tests call runChat without main.
func installTurnSink(rt *runtime.AgentRuntime) {
	rt.SetTurnSink(func(e runtime.TurnEvent) { emit(e) })
}

func chatParams(raw json.RawMessage) (ChatParams, error) {
	var p ChatParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return p, fmt.Errorf("invalid chat params: %w", err)
		}
	}
	return p, nil
}

// runChat is the blocking chat method: it submits through the hub
// (queueing behind a running turn in the same session) and answers the
// request once the turn ends. Events carry the request id as streamId.
func runChat(rt *runtime.AgentRuntime, req Request) {
	installTurnSink(rt)
	streamID := append(json.RawMessage(nil), req.ID...)
	p, err := chatParams(req.Params)
	if err != nil {
		emit(errResponse(streamID, err.Error()))
		return
	}
	_, done, err := rt.SubmitTurn(p.request(), runtime.SubmitOpts{Display: p.Display, StreamID: streamID})
	if err != nil {
		emit(errResponse(streamID, err.Error()))
		return
	}
	final := <-done
	if final.Error != "" {
		emit(errResponse(streamID, final.Error))
		return
	}
	emit(okResponse(streamID, map[string]any{
		"sessionId":     final.SessionID,
		"reply":         final.Reply,
		"stopped":       final.Stopped || final.Cancelled,
		"cancelled":     final.Cancelled,
		"needsContinue": final.NeedsContinue,
	}))
}
