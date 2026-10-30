package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func TestChatForwardsCompactionEvents(t *testing.T) {
	var overflowed int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(string(body), "compacting the conversation"):
			fmt.Fprint(w, `{"choices": [{"message": {"content": "SUM"}}]}`)
		case strings.Contains(string(body), "zz-second-msg") && atomic.AddInt32(&overflowed, 1) == 1:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"This model's maximum context length is 8192 tokens","code":"context_length_exceeded"}}`)
		default:
			fmt.Fprint(w, `{"choices": [{"message": {"content": "hello"}}]}`)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	rt := runtime.WithKeyStore(dir, config.NewConfig(dir), config.NewMemoryKeyStore())
	mustOK(t, callUnary(rt, "1", "addProvider", map[string]string{"name": "mock", "apiUrl": server.URL, "apiKey": "sk-live", "apiType": "openai"}))
	openResp := callUnary(rt, "2", "openSession", map[string]string{"cwd": "/tmp/p4"})
	mustOK(t, openResp)
	data, _ := json.Marshal(openResp.Result)
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(data, &sess)
	chat := func(msg string) string {
		return runTurnOut(t, rt, ChatParams{SessionID: sess.SessionID, Message: msg, Cwd: "/tmp/p4"})
	}
	chat("zz-first-msg")
	out := chat("zz-second-msg")
	var phases []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var msg map[string]any
		_ = json.Unmarshal([]byte(line), &msg)
		if msg["event"] == "chat/compact" {
			info, _ := msg["compaction"].(map[string]any)
			if msg["sessionId"] != sess.SessionID || info == nil {
				t.Fatalf("bad event %v", msg)
			}
			phases = append(phases, fmt.Sprint(info["phase"], "/", info["reason"]))
		}
	}
	if strings.Join(phases, ",") != "start/overflow,done/overflow" {
		t.Fatalf("phases %v in %q", phases, out)
	}
	if !strings.Contains(out, `"reply":"hello"`) {
		t.Fatalf("turn did not recover: %q", out)
	}
}
