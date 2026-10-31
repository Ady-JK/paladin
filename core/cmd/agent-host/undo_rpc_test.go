package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStdioRevertAndRedo(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if i == 0 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"shell","arguments":"{\"cmd\":\"echo agent > a.txt\"}"}}]}}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	h := startLiveHost(t, buildHost(t), t.TempDir())
	h.call("addProvider", map[string]string{"name": "mock", "apiUrl": srv.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("setDefaultProvider", map[string]string{"name": "mock"})
	cwd := t.TempDir()
	sid := h.call("openSession", map[string]string{"cwd": cwd})["result"].(map[string]any)["sessionId"].(string)
	h.call("submitChat", map[string]any{"sessionId": sid, "message": "write it", "cwd": cwd, "permissions": "full-access"})
	h.event("turn/done", sid, nil)

	hist := h.call("sessionHistory", map[string]any{"sessionId": sid})["result"].([]any)
	first := hist[0].(map[string]any)
	if first["role"] != "user" || first["index"] != float64(0) {
		t.Fatalf("user row %v", first)
	}
	pv := h.call("revertPreview", map[string]any{"sessionId": sid, "index": 0})["result"].(map[string]any)
	if files := pv["files"].([]any); len(files) != 1 || files[0] != "a.txt" || pv["snapshots"] != true {
		t.Fatalf("preview %v", pv)
	}
	changes := h.call("sessionChanges", map[string]any{"sessionId": sid})["result"].(map[string]any)
	if files := changes["files"].([]any); len(files) != 1 || files[0].(map[string]any)["status"] != "created" {
		t.Fatalf("session changes %v", changes)
	}
	diff := h.call("sessionDiff", map[string]any{"sessionId": sid, "path": "a.txt", "turn": 0})["result"].(map[string]any)
	if !strings.Contains(diff["patch"].(string), "+agent") {
		t.Fatalf("session diff %v", diff)
	}
	res := h.call("revertSession", map[string]any{"sessionId": sid, "index": 0})["result"].(map[string]any)
	if res["message"] != "write it" {
		t.Fatalf("revert %v", res)
	}
	if _, err := os.Stat(filepath.Join(cwd, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a.txt survived the revert")
	}
	if hist := h.call("sessionHistory", map[string]any{"sessionId": sid})["result"].([]any); len(hist) != 0 {
		t.Fatalf("history after revert %v", hist)
	}
	sess := h.call("openSession", map[string]any{"sessionId": sid})["result"].(map[string]any)
	if rv, _ := sess["revert"].(map[string]any); rv == nil || rv["index"] != float64(0) {
		t.Fatalf("openSession revert %v", sess)
	}
	h.call("unrevertSession", map[string]any{"sessionId": sid})
	if b, _ := os.ReadFile(filepath.Join(cwd, "a.txt")); string(b) != "agent\n" {
		t.Fatalf("redo a.txt = %q", b)
	}
	id := h.send("revertSession", map[string]any{"sessionId": sid})
	if bad := h.waitLine("revert without index", func(m map[string]any) bool { return m["id"] == id }); bad["ok"] == true {
		t.Fatal("revert without index accepted")
	}
}

func TestStdioWorktreeSessionLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"}, {"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	h := startLiveHost(t, buildHost(t), t.TempDir())
	h.call("addProvider", map[string]string{"name": "mock", "apiUrl": srv.URL, "apiKey": "sk", "apiType": "openai"})
	h.call("setDefaultProvider", map[string]string{"name": "mock"})

	gi := h.call("gitInfo", map[string]any{"cwd": repo})["result"].(map[string]any)
	if gi["repo"] != true || gi["branch"] != "main" {
		t.Fatalf("gitInfo %v", gi)
	}
	sess := h.call("openSession", map[string]any{"cwd": repo, "worktree": true, "name": "rpc"})["result"].(map[string]any)
	wt, _ := sess["worktree"].(map[string]any)
	if wt == nil || wt["branch"] != "paladin/rpc" || sess["cwd"] == repo {
		t.Fatalf("openSession %v", sess)
	}
	sid, wcwd := sess["sessionId"].(string), sess["cwd"].(string)
	os.WriteFile(filepath.Join(wcwd, "f.txt"), []byte("x\n"), 0o644)
	st := h.call("worktreeStatus", map[string]any{"sessionId": sid})["result"].(map[string]any)
	if st["changed"] != float64(1) || st["into"] != "main" {
		t.Fatalf("status %v", st)
	}
	fork := h.call("forkSession", map[string]any{"sessionId": sid, "worktree": true, "name": "rpc2"})["result"].(map[string]any)
	if fwt, _ := fork["worktree"].(map[string]any); fwt == nil || fwt["branch"] != "paladin/rpc2" {
		t.Fatalf("fork %v", fork)
	}
	m := h.call("worktreeMerge", map[string]any{"sessionId": sid, "message": "add f"})["result"].(map[string]any)
	if m["merged"] != true {
		t.Fatalf("merge %v", m)
	}
	if _, err := os.Stat(filepath.Join(repo, "f.txt")); err != nil {
		t.Fatal("not merged")
	}
	h.call("deleteSession", map[string]any{"sessionId": sid, "discardWorktree": true})
	if _, err := os.Stat(wcwd); !os.IsNotExist(err) {
		t.Fatal("worktree not cleaned up")
	}
}
