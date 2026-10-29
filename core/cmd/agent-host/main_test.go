package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/runtime"
)

func testRuntime(t *testing.T) *runtime.AgentRuntime {
	t.Helper()
	dir := t.TempDir()
	rt := runtime.WithKeyStore(dir, config.NewConfig(dir), config.NewMemoryKeyStore())
	// Never contact real providers from tests.
	rt.SetModelFetcher(func(context.Context, config.Provider, string) ([]string, error) {
		return []string{"m1"}, nil
	})
	return rt
}

func callUnary(rt *runtime.AgentRuntime, id, method string, params any) Response {
	var raw json.RawMessage
	if params != nil {
		data, _ := json.Marshal(params)
		raw = data
	}
	return runUnary(rt, &Request{ID: json.RawMessage(`"` + id + `"`), Method: method, Params: raw})
}

// runTurnOut submits a turn like the client does and returns every
// stdout line (the submit response, then the turn's events) once the hub
// is idle again.
func runTurnOut(t *testing.T, rt *runtime.AgentRuntime, p ChatParams) string {
	t.Helper()
	installTurnSink(rt)
	return captureStdout(t, func() {
		emit(callUnary(rt, "submit", "submitChat", p))
		deadline := time.Now().Add(20 * time.Second)
		for len(rt.Turns()) > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// turnDone returns the turn of the first turn/done event in out.
func turnDone(t *testing.T, out string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var msg map[string]any
		if json.Unmarshal([]byte(line), &msg) == nil && msg["event"] == "turn/done" {
			turn, _ := msg["turn"].(map[string]any)
			return turn
		}
	}
	t.Fatalf("no turn/done in %q", out)
	return nil
}

func mustOK(t *testing.T, resp Response) {
	t.Helper()
	if !resp.OK {
		msg := ""
		if resp.Error != nil {
			msg = *resp.Error
		}
		t.Fatalf("not ok: %s", msg)
	}
}

func mustFail(t *testing.T, resp Response, contains string) {
	t.Helper()
	if resp.OK {
		t.Fatal("expected failure")
	}
	if resp.Error == nil || !strings.Contains(*resp.Error, contains) {
		t.Fatalf("error %v should contain %q", resp.Error, contains)
	}
}

func TestPing(t *testing.T) {
	mustOK(t, callUnary(testRuntime(t), "1", "ping", nil))
}

func TestUnknownMethod(t *testing.T) {
	mustFail(t, callUnary(testRuntime(t), "1", "frobnicate", nil), `unknown method`)
}

func TestErrResponseDefaultsMessage(t *testing.T) {
	resp := errResponse(json.RawMessage(`"1"`), "")
	if resp.OK || resp.Error == nil || *resp.Error != "unknown error" {
		t.Fatalf("got %+v", resp)
	}
}

func TestListProvidersSeedsHermetic(t *testing.T) {
	resp := callUnary(testRuntime(t), "1", "listProviders", nil)
	mustOK(t, resp)
	data, _ := json.Marshal(resp.Result)
	var infos []config.ProviderInfo
	if err := json.Unmarshal(data, &infos); err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("want 2 seeds, got %+v", infos)
	}
	for _, info := range infos {
		if info.HasKey {
			t.Fatalf("fresh store should have no keys: %+v", info)
		}
		if info.Models == nil {
			t.Fatalf("models nil, want []: %+v", info)
		}
		if info.Name == "Opencode Go" && !info.Default {
			t.Fatalf("seed default wrong: %+v", infos)
		}
	}
}

func TestRefreshProvidersStarts(t *testing.T) {
	rt := testRuntime(t)
	resp := callUnary(rt, "1", "refreshProviders", nil)
	mustOK(t, resp)
	rt.WaitRefresh()
}

func TestRefreshOneProvider(t *testing.T) {
	rt := testRuntime(t)
	resp := callUnary(rt, "1", "refreshProviders", map[string]string{"name": "openrouter"})
	mustOK(t, resp)
	res, ok := resp.Result.(runtime.RefreshResult)
	if !ok || res.Name != "openrouter" || res.Count != 1 {
		t.Fatalf("unexpected result %#v", resp.Result)
	}
	mustFail(t, callUnary(rt, "2", "refreshProviders", map[string]string{"name": "missing"}), "not found")
}

func TestSetDefaultProviderValidation(t *testing.T) {
	rt := testRuntime(t)
	mustFail(t, callUnary(rt, "1", "setDefaultProvider", map[string]string{"name": ""}), "required")
	mustFail(t, callUnary(rt, "1", "setDefaultProvider", map[string]string{"name": "missing"}), "not found")
	mustOK(t, callUnary(rt, "1", "setDefaultProvider", map[string]string{"name": "openrouter"}))
}

func TestAddUpdateProviderValidation(t *testing.T) {
	rt := testRuntime(t)
	mustFail(t, callUnary(rt, "1", "addProvider", map[string]string{"name": "", "apiUrl": "https://x/v1", "apiKey": "k"}), "required")
	mustFail(t, callUnary(rt, "2", "addProvider", nil), "required")
	ok := callUnary(rt, "3", "addProvider", map[string]string{"name": "vault", "apiUrl": "https://x/v1", "apiKey": "sk-v", "apiType": "openai"})
	mustOK(t, ok)
	mustFail(t, callUnary(rt, "4", "updateProvider", map[string]string{"name": "missing", "apiUrl": "https://x/v1", "apiType": "openai"}), "not found")
	mustFail(t, callUnary(rt, "5", "updateProvider", map[string]string{"name": ""}), "required")
	upd := callUnary(rt, "6", "updateProvider", map[string]string{"name": "vault", "apiUrl": "https://y/v2", "apiType": "openai"})
	mustOK(t, upd)
}

func TestWorkspaceRoundTrip(t *testing.T) {
	rt := testRuntime(t)
	resp := callUnary(rt, "1", "getWorkspace", nil)
	mustOK(t, resp)
	dir := t.TempDir()
	setResp := callUnary(rt, "2", "setWorkspace", map[string]string{"path": dir})
	mustOK(t, setResp)
	if s, _ := setResp.Result.(string); s != dir {
		t.Fatalf("got %v", setResp.Result)
	}
	mustFail(t, callUnary(rt, "3", "setWorkspace", map[string]string{"path": filepath.Join(dir, "missing")}), "not reachable")
	// resolveWorkspace: explicit cwd wins, else home.
	got := callUnary(rt, "4", "resolveWorkspace", map[string]string{"cwd": "/tmp/x"})
	mustOK(t, got)
	if s, _ := got.Result.(string); s != "/tmp/x" {
		t.Fatalf("got %v", got.Result)
	}
	home := callUnary(rt, "5", "resolveWorkspace", nil)
	mustOK(t, home)
	if s, _ := home.Result.(string); s == "" {
		t.Fatal("empty home")
	}
	if h := callUnary(rt, "6", "fsHome", nil); !h.OK {
		t.Fatal("fsHome failed")
	}
}

func TestFsList(t *testing.T) {
	rt := testRuntime(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	resp := callUnary(rt, "1", "fsList", map[string]string{"path": dir})
	mustOK(t, resp)
	mustFail(t, callUnary(rt, "2", "fsList", map[string]string{"path": filepath.Join(dir, "missing")}), "does not exist")
}

func TestSessionLifecycleWithoutKey(t *testing.T) {
	rt := testRuntime(t)
	// No usable provider: openSession must fail before touching disk.
	mustFail(t, callUnary(rt, "1", "openSession", map[string]string{"cwd": "/tmp/p"}), "placeholder")
	mustFail(t, callUnary(rt, "2", "openSession", map[string]string{"sessionId": "nope", "cwd": "/tmp/p"}), "not found")
	mustOK(t, callUnary(rt, "3", "listSessions", map[string]string{"cwd": "/tmp/p"}))
	mustFail(t, callUnary(rt, "4", "sessionHistory", map[string]string{"sessionId": "", "cwd": "/tmp/p"}), "required")
	mustFail(t, callUnary(rt, "5", "deleteSession", map[string]string{"sessionId": "nope", "cwd": "/tmp/p"}), "not found")
	// stopChat on idle runtime reports false.
	resp := callUnary(rt, "6", "stopChat", map[string]string{"sessionId": "s"})
	mustOK(t, resp)
	if b, _ := resp.Result.(bool); b {
		t.Fatal("idle stop should be false")
	}
}

func TestArgsAreLenientPerField(t *testing.T) {
	if got := parseArgs(nil).str("x"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := parseArgs(json.RawMessage(`{bad`)).str("x"); got != "" {
		t.Fatalf("got %q", got)
	}
	a := parseArgs(json.RawMessage(`{"x":123,"s":"v","limit":5,"bad":"x","on":true,"off":"yes"}`))
	if got := a.str("x"); got != "" {
		t.Fatalf("non-string should be empty, got %q", got)
	}
	if got := a.str("s"); got != "v" {
		t.Fatalf("got %q", got)
	}
	if got := parseArgs(nil).num("limit", 100); got != 100 {
		t.Fatalf("got %d", got)
	}
	if got := a.num("limit", 100); got != 5 {
		t.Fatalf("got %d", got)
	}
	if got := a.num("bad", 100); got != 100 {
		t.Fatalf("got %d", got)
	}
	if !a.flag("on") || a.flag("off") || a.flag("missing") {
		t.Fatal("flag: only a JSON true is true")
	}
	var into struct {
		S string `json:"s"`
	}
	if err := a.decode(&into); err != nil || into.S != "v" {
		t.Fatalf("decode: %v %+v", err, into)
	}
	if err := parseArgs(nil).decode(&into); err != nil {
		t.Fatalf("empty params decode: %v", err)
	}
}

// Every method PROTOCOL.md documents must be dispatchable, and nothing
// undocumented may sneak into the table.
func TestEveryDocumentedMethodIsRegistered(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "desktop-app", "PROTOCOL.md"))
	if err != nil {
		t.Skip("PROTOCOL.md not found:", err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z][a-zA-Z0-9]*)` \\|").FindAllStringSubmatch(string(doc), -1) {
		documented[m[1]] = true
	}
	if len(documented) < 40 {
		t.Fatalf("parsed only %d methods from PROTOCOL.md", len(documented))
	}
	for name := range documented {
		if _, ok := methods[name]; !ok {
			t.Errorf("documented method %q is not registered", name)
		}
	}
	for name := range methods {
		if !documented[name] {
			t.Errorf("registered method %q is not documented in PROTOCOL.md", name)
		}
	}
}

func TestUnknownMethodFails(t *testing.T) {
	rt := testRuntime(t)
	mustFail(t, callUnary(rt, "9", "no_such_method", nil), `unknown method "no_such_method"`)
}

func TestSubmitChatRejectsAMissingSession(t *testing.T) {
	rt := testRuntime(t)
	mustFail(t, callUnary(rt, "7", "submitChat", ChatParams{Message: "hi", Cwd: "/tmp/p"}), "sessionId is required")
}

func TestChatFullTurnInProcess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "hello"}}]}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	rt := runtime.WithKeyStore(dir, config.NewConfig(dir), config.NewMemoryKeyStore())
	mustOK(t, callUnary(rt, "1", "addProvider", map[string]string{"name": "mock", "apiUrl": server.URL, "apiKey": "sk-live", "apiType": "openai"}))
	openResp := callUnary(rt, "2", "openSession", map[string]string{"cwd": "/tmp/p2"})
	mustOK(t, openResp)
	data, _ := json.Marshal(openResp.Result)
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &sess); err != nil || sess.SessionID == "" {
		t.Fatalf("open: %v %s", err, data)
	}
	out := runTurnOut(t, rt, ChatParams{SessionID: sess.SessionID, Message: "hi", Cwd: "/tmp/p2"})
	var deltas int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		if msg["event"] == "chat/delta" {
			deltas++
			if msg["delta"] != "hello" || msg["sessionId"] != sess.SessionID {
				t.Fatalf("delta: %v", msg)
			}
		}
	}
	if deltas != 1 {
		t.Fatalf("want 1 delta, got %d in %q", deltas, out)
	}
	if turn := turnDone(t, out); turn["reply"] != "hello" || turn["sessionId"] != sess.SessionID || turn["error"] != nil {
		t.Fatalf("got %+v", turn)
	}
	// History persisted through the protocol.
	hist := callUnary(rt, "10", "sessionHistory", map[string]string{"sessionId": sess.SessionID, "cwd": "/tmp/p2"})
	mustOK(t, hist)
	hdata, _ := json.Marshal(hist.Result)
	var rows []any
	if err := json.Unmarshal(hdata, &rows); err != nil || len(rows) != 2 {
		t.Fatalf("history: %v %s", err, hdata)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	data, _ := readAllPipe(r)
	_ = r.Close()
	return string(data)
}

func readAllPipe(r *os.File) ([]byte, error) {
	var out []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			out = append(out, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return out, nil
}

// buildHost compiles the agent-host binary for end-to-end stdio tests.
func buildHost(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agent-host")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

type hostClient struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser
	scan  *bufio.Scanner
}

func startHost(t *testing.T, bin, dataDir string) *hostClient {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "PALADIN_DATA_DIR="+dataDir)
	// Keep the protocol tests hermetic: the host normally reads the real
	// OS keychain, where a developer machine can have live provider keys.
	// The opt-in live keychain test needs the OS store instead.
	if os.Getenv("PALADIN_KEYRING_LIVE") != "1" {
		cmd.Env = append(cmd.Env, "PALADIN_KEYSTORE=memory")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	return &hostClient{t: t, cmd: cmd, stdin: stdin, scan: bufio.NewScanner(stdout)}
}

func (h *hostClient) call(id, method string, params any) map[string]any {
	h.t.Helper()
	var raw []byte
	if params != nil {
		var err error
		raw, err = json.Marshal(params)
		if err != nil {
			h.t.Fatal(err)
		}
	} else {
		raw = []byte("{}")
	}
	line := fmt.Sprintf(`{"id":%q,"method":%q,"params":%s}`+"\n", id, method, raw)
	if _, err := h.stdin.Write([]byte(line)); err != nil {
		h.t.Fatal(err)
	}
	for h.scan.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(h.scan.Bytes(), &msg); err != nil {
			h.t.Fatalf("bad line %q", h.scan.Text())
		}
		if mid, _ := msg["id"].(string); mid == id {
			return msg
		}
		// Stream events interleave; chat tests read them explicitly.
	}
	h.t.Fatalf("no response for %s", method)
	return nil
}

func TestEndToEndStdioProtocol(t *testing.T) {
	bin := buildHost(t)
	dir := t.TempDir()
	h := startHost(t, bin, dir)
	if msg := h.call("1", "ping", nil); msg["ok"] != true {
		t.Fatalf("ping: %v", msg)
	}
	if msg := h.call("2", "listProviders", nil); msg["ok"] != true {
		t.Fatalf("list: %v", msg)
	} else if arr, _ := msg["result"].([]any); len(arr) != 2 {
		t.Fatalf("seeds: %v", msg)
	}
	if msg := h.call("3", "nope", nil); msg["ok"] != false || !strings.Contains(msg["error"].(string), "unknown method") {
		t.Fatalf("unknown: %v", msg)
	}
	// Malformed line is logged and skipped; the host stays alive.
	if _, err := h.stdin.Write([]byte("{not json\n")); err != nil {
		t.Fatal(err)
	}
	if msg := h.call("4", "ping", nil); msg["ok"] != true {
		t.Fatalf("host died after bad line: %v", msg)
	}
	// Blank line is skipped silently.
	if _, err := h.stdin.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	if msg := h.call("5", "ping", nil); msg["ok"] != true {
		t.Fatalf("host died after blank line: %v", msg)
	}
	if msg := h.call("6", "openSession", map[string]string{"cwd": "/tmp/e2e"}); msg["ok"] != false {
		t.Fatalf("keyless open should fail: %v", msg)
	}
	if msg := h.call("7", "setWorkspace", map[string]string{"path": dir}); msg["ok"] != true {
		t.Fatalf("setWorkspace: %v", msg)
	}
	if msg := h.call("8", "getWorkspace", nil); msg["ok"] != true || msg["result"] != dir {
		t.Fatalf("getWorkspace: %v", msg)
	}
	if msg := h.call("9", "fsList", map[string]string{"path": dir}); msg["ok"] != true {
		t.Fatalf("fsList: %v", msg)
	}
	if msg := h.call("10", "stopChat", map[string]string{"sessionId": "x"}); msg["ok"] != true || msg["result"] != false {
		t.Fatalf("stopChat: %v", msg)
	}
	if msg := h.call("11", "deleteSession", map[string]string{"sessionId": "nope", "cwd": dir}); msg["ok"] != false {
		t.Fatalf("delete missing should fail: %v", msg)
	}
	// Invalid params shape still yields an error response, not a crash.
	if _, err := h.stdin.Write([]byte("{\"id\":\"12\",\"method\":\"addProvider\",\"params\":[1,2]}\n")); err != nil {
		t.Fatal(err)
	}
	for h.scan.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(h.scan.Bytes(), &msg); err != nil {
			t.Fatalf("bad line %q", h.scan.Text())
		}
		if mid, _ := msg["id"].(string); mid == "12" {
			if msg["ok"] != false {
				t.Fatalf("array params should fail: %v", msg)
			}
			break
		}
	}
}

func TestEndToEndChatOverStdio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "hi there"}}]}`)
	}))
	defer server.Close()
	bin := buildHost(t)
	dir := t.TempDir()
	// Seed catalog pointing at the mock server; key goes to the live
	// keychain only under the opt-in flag.
	if os.Getenv("PALADIN_KEYRING_LIVE") != "1" {
		t.Skip("needs the real Secret Service for provider keys")
	}
	ks := config.OsKeyStore{}
	provider := "e2e-chat"
	t.Cleanup(func() { _ = ks.Delete(provider) })
	if err := ks.Set(provider, "sk-e2e"); err != nil {
		t.Fatalf("keychain: %v", err)
	}
	store := config.NewStore(dir, ks)
	file, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	file.Providers = append(file.Providers, config.Provider{Name: provider, ApiURL: server.URL, ApiType: config.ApiTypeOpenai})
	if err := store.SaveProviders(file); err != nil {
		t.Fatal(err)
	}
	h := startHost(t, bin, dir)
	open := h.call("1", "openSession", map[string]string{"cwd": dir})
	if open["ok"] != true {
		t.Fatalf("open: %v", open)
	}
	sessID := open["result"].(map[string]any)["sessionId"].(string)
	// submitChat answers at once; the turn streams deltas, then turn/done.
	if _, err := h.stdin.Write(fmt.Appendf(nil,
		`{"id":"2","method":"submitChat","params":{"sessionId":%q,"message":"hi","cwd":%q}}`+"\n", sessID, dir)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	var sawDelta, sawFinal bool
	for !(sawDelta && sawFinal) {
		if time.Now().After(deadline) {
			t.Fatalf("chat timed out (delta=%v final=%v)", sawDelta, sawFinal)
		}
		if !h.scan.Scan() {
			t.Fatal("host closed stdout mid-chat")
		}
		var msg map[string]any
		if err := json.Unmarshal(h.scan.Bytes(), &msg); err != nil {
			t.Fatalf("bad line %q", h.scan.Text())
		}
		if msg["event"] == "chat/delta" {
			sawDelta = true
		}
		if mid, _ := msg["id"].(string); mid == "2" && msg["ok"] != true {
			t.Fatalf("submit failed: %v", msg)
		}
		if msg["event"] == "turn/done" {
			sawFinal = true
			turn := msg["turn"].(map[string]any)
			if turn["reply"] != "hi there" || turn["sessionId"] != sessID {
				t.Fatalf("turn: %v", msg)
			}
		}
	}
	hist := h.call("3", "sessionHistory", map[string]string{"sessionId": sessID, "cwd": dir})
	if hist["ok"] != true {
		t.Fatalf("history: %v", hist)
	}
	if arr, _ := hist["result"].([]any); len(arr) != 2 {
		t.Fatalf("history rows: %v", hist)
	}
}

func TestSessionUsageMethod(t *testing.T) {
	rt := testRuntime(t)
	mustFail(t, callUnary(rt, "1", "sessionUsage", map[string]string{"sessionId": "", "cwd": "/tmp/p"}), "required")
	mustFail(t, callUnary(rt, "2", "sessionUsage", map[string]string{"sessionId": "nope", "cwd": "/tmp/p"}), "not found")
}
