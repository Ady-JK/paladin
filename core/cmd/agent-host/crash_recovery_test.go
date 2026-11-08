package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStdioRecoversKilledTurnAndContinues(t *testing.T) {
	var calls atomic.Int32
	secondRound := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"job1","function":{"name":"shell","arguments":"{\"cmd\":\"echo completed >> marker.txt\"}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
		case 2:
			close(secondRound)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			if !strings.Contains(string(body), "interrupted") || !strings.Contains(string(body), "marker.txt") {
				t.Errorf("continuation missing recovered context")
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"continued safely\"}}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	bin, dataDir, cwd := buildHost(t), t.TempDir(), t.TempDir()
	h := startLiveHost(t, bin, dataDir)
	h.call("addProvider", map[string]string{"name": "mock", "apiUrl": server.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("setDefaultProvider", map[string]string{"name": "mock"})
	sid := h.call("openSession", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)
	h.call("submitChat", map[string]any{"sessionId": sid, "cwd": cwd, "message": "create marker", "permissions": "full-access"})
	h.event("chat/tool", sid, func(m map[string]any) bool {
		tool, _ := m["tool"].(map[string]any)
		return tool["name"] == "shell" && tool["status"] == "ok"
	})
	select {
	case <-secondRound:
	case <-time.After(10 * time.Second):
		t.Fatal("second model round did not start")
	}
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("sidecar did not stop")
	}
	h = startLiveHost(t, bin, dataDir)
	h.call("addProvider", map[string]string{"name": "mock2", "apiUrl": server.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("openSession", map[string]string{"sessionId": sid})
	history := h.call("sessionHistory", map[string]string{"sessionId": sid})["result"]
	raw, _ := json.Marshal(history)
	if !strings.Contains(string(raw), "create marker") || !strings.Contains(string(raw), "interrupted") || !strings.Contains(string(raw), "shell") {
		t.Fatalf("recovered history: %s", raw)
	}
	h.call("openSession", map[string]string{"sessionId": sid})
	again, _ := json.Marshal(h.call("sessionHistory", map[string]string{"sessionId": sid})["result"])
	if string(raw) != string(again) {
		t.Fatal("reopening duplicated interrupted turn")
	}
	h.call("submitChat", map[string]any{"sessionId": sid, "cwd": cwd, "message": "continue safely", "provider": "mock2", "permissions": "full-access"})
	h.event("turn/done", sid, nil)
	data, err := os.ReadFile(filepath.Join(cwd, "marker.txt"))
	if err != nil || string(data) != "completed\n" {
		t.Fatalf("command repeated or lost: %q %v", data, err)
	}
}

func TestStdioMarksInterruptedToolAsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"job1","function":{"name":"shell","arguments":"{\"cmd\":\"echo started > marker.txt; sleep 2\"}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	bin, dataDir, cwd := buildHost(t), t.TempDir(), t.TempDir()
	h := startLiveHost(t, bin, dataDir)
	h.call("addProvider", map[string]string{"name": "mock", "apiUrl": server.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("setDefaultProvider", map[string]string{"name": "mock"})
	sid := h.call("openSession", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)
	h.call("submitChat", map[string]any{"sessionId": sid, "cwd": cwd, "message": "run command", "permissions": "full-access"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(cwd, "marker.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("sidecar did not stop")
	}
	h = startLiveHost(t, bin, dataDir)
	h.call("openSession", map[string]string{"sessionId": sid})
	raw, _ := json.Marshal(h.call("sessionHistory", map[string]string{"sessionId": sid})["result"])
	if !strings.Contains(string(raw), "may have run") || !strings.Contains(string(raw), "interrupted") {
		t.Fatalf("unknown tool outcome missing: %s", raw)
	}
}

func TestStdioRecoversCompletedParallelToolBeforeBatchFinishes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"fast","function":{"name":"shell","arguments":"{\"cmd\":\"echo fast-complete\"}"}},{"index":1,"id":"slow","function":{"name":"shell","arguments":"{\"cmd\":\"echo started > marker.txt; sleep 3\"}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	bin, dataDir, cwd := buildHost(t), t.TempDir(), t.TempDir()
	h := startLiveHost(t, bin, dataDir)
	h.call("addProvider", map[string]string{"name": "mock", "apiUrl": server.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("setDefaultProvider", map[string]string{"name": "mock"})
	sid := h.call("openSession", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)
	h.call("submitChat", map[string]any{"sessionId": sid, "cwd": cwd, "message": "run parallel commands", "permissions": "full-access"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		paths, _ := filepath.Glob(filepath.Join(dataDir, "sessions", "*", sid, "active-turn.json"))
		if len(paths) == 1 {
			data, _ := os.ReadFile(paths[0])
			_, markerErr := os.Stat(filepath.Join(cwd, "marker.txt"))
			if markerErr == nil && strings.Contains(string(data), "fast-complete") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("parallel checkpoint not written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("sidecar did not stop")
	}
	h = startLiveHost(t, bin, dataDir)
	h.call("openSession", map[string]string{"sessionId": sid})
	raw, _ := json.Marshal(h.call("sessionHistory", map[string]string{"sessionId": sid})["result"])
	if !strings.Contains(string(raw), "fast-complete") || !strings.Contains(string(raw), "may have run") {
		t.Fatalf("parallel tool recovery missing: %s", raw)
	}
}
