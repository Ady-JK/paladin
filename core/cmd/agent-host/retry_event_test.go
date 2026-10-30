package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func TestChatForwardsRetryEvents(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":{"message":"overloaded"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "hello"}}]}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	rt := runtime.WithKeyStore(dir, config.NewConfig(dir), config.NewMemoryKeyStore())
	mustOK(t, callUnary(rt, "1", "addProvider", map[string]string{"name": "mock", "apiUrl": server.URL, "apiKey": "sk-live", "apiType": "openai"}))
	openResp := callUnary(rt, "2", "openSession", map[string]string{"cwd": "/tmp/p3"})
	mustOK(t, openResp)
	data, _ := json.Marshal(openResp.Result)
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(data, &sess)
	out := runTurnOut(t, rt, ChatParams{SessionID: sess.SessionID, Message: "hi", Cwd: "/tmp/p3"})
	var retry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var msg map[string]any
		_ = json.Unmarshal([]byte(line), &msg)
		if msg["event"] == "chat/retry" {
			retry = msg
		}
	}
	if retry == nil {
		t.Fatalf("no chat/retry event in %q", out)
	}
	info, _ := retry["retry"].(map[string]any)
	if info == nil || info["attempt"] != float64(1) || !strings.Contains(fmt.Sprint(info["reason"]), "503") || retry["sessionId"] != sess.SessionID {
		t.Fatalf("bad retry event %v", retry)
	}
	if !strings.Contains(out, `"reply":"hello"`) {
		t.Fatalf("turn did not recover: %q", out)
	}
}
