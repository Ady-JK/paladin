package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
)

func testRuntime(t *testing.T) (*AgentRuntime, string) {
	t.Helper()
	dir := t.TempDir()
	return WithKeyStore(dir, config.NewConfig(dir), config.NewMemoryKeyStore()), dir
}

func pointMockAt(rt *AgentRuntime, url string) {
	rt.providersMu.Lock()
	defer rt.providersMu.Unlock()
	file, err := rt.store.LoadProviders()
	if err != nil {
		panic(err)
	}
	for i, p := range file.Providers {
		if p.Name == "openrouter" {
			file.Providers[i].ApiURL = url
		}
	}
	if err := rt.store.SaveProviders(file); err != nil {
		panic(err)
	}
}

func chatToEnd(t *testing.T, rt *AgentRuntime, req agent.Request) (agent.ChatOutcome, []agent.ChatChunk) {
	t.Helper()
	tx := make(chan agent.ChatChunk, 256)
	done := make(chan struct{})
	defer close(done)
	var chunks []agent.ChatChunk
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for c := range tx {
			chunks = append(chunks, c)
		}
	}()
	outcome, err := rt.Chat(req, tx, done)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return outcome, chunks
}

func TestOpenListHistoryDeleteSession(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if sess.SessionID == "" {
		t.Fatal("empty session id")
	}
	list, err := rt.ListSessions("/tmp/proj", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d", len(list))
	}
	history, err := rt.SessionHistory(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("got %d", len(history))
	}
	ok, err := rt.DeleteSession(sess.SessionID, "/tmp/proj")
	if err != nil || !ok {
		t.Fatalf("delete %v %v", ok, err)
	}
}

type failingKeyStore struct{ err error }

func (f failingKeyStore) Get(string) (string, bool, error) { return "", false, f.err }
func (f failingKeyStore) Set(string, string) error         { return f.err }
func (f failingKeyStore) Delete(string) error              { return nil }

// A keychain failure while adding a provider must not leave an entry in
// the catalog that has no secret behind it.
func TestAddProviderKeychainFailureLeavesCatalogUntouched(t *testing.T) {
	dir := t.TempDir()
	rt := WithKeyStore(dir, config.NewConfig(dir), failingKeyStore{err: fmt.Errorf("keychain unavailable: no bus")})
	before, err := rt.store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.AddProvider(config.ProviderInput{
		Name:    "vault",
		ApiURL:  "https://example.test/v1",
		ApiKey:  "sk-never-stored",
		ApiType: "openai",
	})
	if err == nil {
		t.Fatal("AddProvider succeeded without a keychain")
	}
	after, err := rt.store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Providers) != len(before.Providers) {
		t.Fatalf("catalog gained providers: %d -> %d", len(before.Providers), len(after.Providers))
	}
	for _, p := range after.Providers {
		if p.Name == "vault" {
			t.Fatal("provider was written despite the keychain failure")
		}
	}
}

func TestConcurrentTurnRejected(t *testing.T) {
	rt, _ := testRuntime(t)
	noop := func() {}
	if !rt.tracker.track("s", "/tmp", noop) {
		t.Fatal("first track must succeed")
	}
	if rt.tracker.track("s", "/tmp", noop) {
		t.Fatal("second track must fail")
	}
	rt.tracker.untrack("s")
}

func TestFullTurnWithMockProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "done"}}]}`)
	}))
	defer server.Close()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	// Point openrouter at the mock server.
	rt.providersMu.Lock()
	file, err := rt.store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range file.Providers {
		if p.Name == "openrouter" {
			file.Providers[i].ApiURL = server.URL
		}
	}
	if err := rt.store.SaveProviders(file); err != nil {
		t.Fatal(err)
	}
	rt.providersMu.Unlock()
	sess, err := rt.OpenSession("", "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Provider != "openrouter" {
		t.Fatalf("provider %q", sess.Provider)
	}
	tx := make(chan agent.ChatChunk, 64)
	done := make(chan struct{})
	defer close(done)
	req := agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: "/tmp/proj"}
	go func() {
		for range tx {
		}
	}()
	outcome, err := rt.Chat(req, tx, done)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reply != "done" {
		t.Fatalf("reply %q", outcome.Reply)
	}
	history, err := rt.SessionHistory(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d", len(history))
	}
}

func TestChatValidation(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	newTx := func() (chan agent.ChatChunk, chan struct{}) {
		return make(chan agent.ChatChunk, 8), make(chan struct{})
	}
	// Missing session id.
	tx, done := newTx()
	if _, err := rt.Chat(agent.Request{Message: "hi", Cwd: "/tmp/p"}, tx, done); err == nil {
		t.Fatal("missing session accepted")
	}
	close(done)
	// Missing message without resume.
	sess, err := rt.OpenSession("", "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	tx, done = newTx()
	if _, err := rt.Chat(agent.Request{SessionID: sess.SessionID, Cwd: "/tmp/p"}, tx, done); err == nil {
		t.Fatal("empty message accepted")
	}
	close(done)
	// Unknown session.
	tx, done = newTx()
	if _, err := rt.Chat(agent.Request{SessionID: "00000000-0000-4000-8000-000000000099", Message: "hi", Cwd: "/tmp/p"}, tx, done); err == nil {
		t.Fatal("unknown session accepted")
	}
	close(done)
	// No usable provider (fresh runtime without keys).
	rt2, _ := testRuntime(t)
	sess2, err := rt2.transcripts.Create("/tmp/p", "openrouter", "m")
	if err != nil {
		t.Fatal(err)
	}
	tx, done = newTx()
	if _, err := rt2.Chat(agent.Request{SessionID: sess2.Meta.SessionID, Message: "hi", Cwd: "/tmp/p"}, tx, done); err == nil {
		t.Fatal("keyless chat accepted")
	}
	close(done)
}

func TestChatStreamsToolRoundEndToEnd(t *testing.T) {
	// First model round returns a read call for a real file, later rounds
	// return text. Exercises the full runtime.Chat -> agent -> tools path.
	wsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(wsDir, "note.txt"), []byte("secret sauce\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls int64
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n == 0 {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"%s/note.txt\\\"}\"}}]}}]}\n\n", wsDir)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"saw it\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, server.URL)
	sess, err := rt.OpenSession("", wsDir)
	if err != nil {
		t.Fatal(err)
	}
	outcome, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "read it", Cwd: wsDir})
	if outcome.Reply != "saw it" {
		t.Fatalf("got %+v", outcome)
	}
	var toolEvents int
	for _, c := range chunks {
		if c.Tool != nil {
			toolEvents++
			if c.Tool.Status == "ok" && !strings.Contains(c.Tool.Result, "secret sauce") {
				t.Fatalf("tool result not wired: %+v", c.Tool)
			}
		}
	}
	if toolEvents < 2 {
		t.Fatalf("want running+done tool events, got %d in %+v", toolEvents, chunks)
	}
	history, err := rt.SessionHistory(sess.SessionID, wsDir)
	if err != nil {
		t.Fatal(err)
	}
	// user, assistant-with-call, tool result, assistant text.
	if len(history) != 4 {
		t.Fatalf("got %d: %+v", len(history), history)
	}
}

func TestChatPlainTextTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "done"}}]}`)
	}))
	defer server.Close()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, server.URL)
	sess, err := rt.OpenSession("", "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	outcome, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: "/tmp/proj"})
	if outcome.Reply != "done" {
		t.Fatalf("got %+v", outcome)
	}
	// Final chunk is the done marker.
	if len(chunks) == 0 || !chunks[len(chunks)-1].Done {
		t.Fatalf("no done chunk: %+v", chunks)
	}
	// History now has user + assistant.
	history, err := rt.SessionHistory(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d", len(history))
	}
}

func TestChatConcurrentTurnRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "done"}}]}`)
	}))
	defer server.Close()
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, server.URL)
	sess, err := rt.OpenSession("", "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	// Hold the tracker directly: second Chat must fail fast.
	rt.tracker.track(sess.SessionID, "/tmp/proj", func() {})
	tx := make(chan agent.ChatChunk, 8)
	done := make(chan struct{})
	defer close(done)
	if _, err := rt.Chat(agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: "/tmp/proj"}, tx, done); err == nil {
		t.Fatal("concurrent turn accepted")
	}
	<-tx // consume the closed channel artifact? Chat defers close(tx) even on error.
}

func TestStopAndStopIn(t *testing.T) {
	rt, _ := testRuntime(t)
	noop := func() {}
	if rt.Stop("missing") {
		t.Fatal("stop false positive")
	}
	rt.tracker.track("s1", "/tmp/a", noop)
	rt.tracker.track("s2", "/tmp/b", noop)
	// Stop cancels but keeps the entry (idempotent, like the Rust core).
	if !rt.Stop("s1") {
		t.Fatal("first stop failed")
	}
	if !rt.Stop("s1") {
		t.Fatal("stop should be idempotent")
	}
	if !rt.StopIn("", "/tmp/b") {
		t.Fatal("stop-in by cwd")
	}
	if rt.StopIn("", "/tmp/nowhere") {
		t.Fatal("stop-in false positive")
	}
	// StopIn with session id ignores cwd.
	rt.tracker.track("s3", "/tmp/c", noop)
	if !rt.StopIn("s3", "/tmp/other") {
		t.Fatal("stop-in by id")
	}
}

func TestOpenSessionValidation(t *testing.T) {
	rt, _ := testRuntime(t)
	if _, err := rt.OpenSession("", ""); err == nil {
		t.Fatal("empty cwd accepted")
	}
	if _, err := rt.OpenSession("00000000-0000-4000-8000-000000000099", "/tmp/p"); err == nil {
		t.Fatal("unknown session accepted")
	}
	if _, err := rt.OpenSession("not-a-uuid", "/tmp/p"); err == nil {
		t.Fatal("bad id accepted")
	}
	// Reopen returns the stored provider/model.
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	again, err := rt.OpenSession(sess.SessionID, "/tmp/p")
	if err != nil || again.SessionID != sess.SessionID || again.Provider != sess.Provider {
		t.Fatalf("got %+v %v", again, err)
	}
}

func TestSessionHistoryValidation(t *testing.T) {
	rt, _ := testRuntime(t)
	if _, err := rt.SessionHistory("", "/tmp/p"); err == nil {
		t.Fatal("empty id accepted")
	}
	if _, err := rt.SessionHistory("nope", "/tmp/p"); err == nil {
		t.Fatal("bad id accepted")
	}
	if _, err := rt.DeleteSession("", "/tmp/p"); err == nil {
		t.Fatal("empty delete accepted")
	}
	if _, err := rt.DeleteSession("nope", "/tmp/p"); err == nil {
		t.Fatal("bad delete accepted")
	}
	// Delete then history fails.
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := rt.DeleteSession(sess.SessionID, "/tmp/p")
	if err != nil || !ok {
		t.Fatalf("delete %v %v", ok, err)
	}
	if _, err := rt.SessionHistory(sess.SessionID, "/tmp/p"); err == nil {
		t.Fatal("history of deleted session")
	}
}

func TestSetDefaultProvider(t *testing.T) {
	rt, _ := testRuntime(t)
	if _, err := rt.SetDefaultProvider(""); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := rt.SetDefaultProvider("missing"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	infos, err := rt.SetDefaultProvider("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range infos {
		if i.Name == "openrouter" && i.Default {
			found = true
		}
	}
	if !found {
		t.Fatalf("got %+v", infos)
	}
}

func TestAddUpdateProviderValidation(t *testing.T) {
	rt, _ := testRuntime(t)
	if _, err := rt.AddProvider(config.ProviderInput{Name: "", ApiURL: "https://x/v1", ApiKey: "k", ApiType: "openai"}); err == nil {
		t.Fatal("empty name accepted")
	}
	info, err := rt.AddProvider(config.ProviderInput{Name: "vault", ApiURL: "https://x/v1", ApiKey: "sk-v", ApiType: "openai"})
	if err != nil || !info.HasKey {
		t.Fatalf("got %+v %v", info, err)
	}
	if _, err := rt.AddProvider(config.ProviderInput{Name: "VAULT", ApiURL: "https://x/v1", ApiKey: "sk-v2", ApiType: "openai"}); err == nil {
		t.Fatal("case-insensitive duplicate accepted")
	}
	updated, err := rt.UpdateProvider(config.ProviderInput{Name: "vault", ApiURL: "https://y/v2", ApiKey: "", ApiType: "openai"})
	if err != nil || updated.ApiURL != "https://y/v2" || !updated.HasKey {
		t.Fatalf("empty key should keep stored key: %+v %v", updated, err)
	}
	updated, err = rt.UpdateProvider(config.ProviderInput{Name: "vault", ApiURL: "https://z/v3", ApiKey: "sk-new", ApiType: "openai"})
	if err != nil || updated.ApiURL != "https://z/v3" {
		t.Fatalf("got %+v %v", updated, err)
	}
	if _, err := rt.UpdateProvider(config.ProviderInput{Name: "", ApiURL: "https://z/v3", ApiType: "openai"}); err == nil {
		t.Fatal("empty update name accepted")
	}
	if _, err := rt.UpdateProvider(config.ProviderInput{Name: "missing", ApiURL: "https://z/v3", ApiType: "openai"}); err == nil {
		t.Fatal("unknown update accepted")
	}
	// Cache eviction: resolve, add provider, resolve again sees the new one.
	if _, _, _, err := rt.resolveProvider("vault", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AddProvider(config.ProviderInput{Name: "second", ApiURL: "https://s/v1", ApiKey: "sk-s", ApiType: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := rt.resolveProvider("second", ""); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProviderModelOverrideAndCache(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	e1, _, p1, err := rt.resolveProvider("", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if e1.Model != "model-a" {
		t.Fatalf("override lost: %+v", e1)
	}
	_, _, p2, err := rt.resolveProvider("", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Fatal("cache miss for identical key")
	}
	_, _, _, err = rt.resolveProvider("", "model-b")
	if err != nil {
		t.Fatal(err)
	}
	// Unknown provider.
	if _, _, _, err := rt.resolveProvider("missing", ""); err == nil {
		t.Fatal("unknown accepted")
	}
	// Placeholder key rejected.
	rt2, _ := testRuntime(t)
	if _, _, _, err := rt2.resolveProvider("", ""); err == nil {
		t.Fatal("placeholder accepted")
	}
}

func TestSystemPromptToolGuidanceMaxRounds(t *testing.T) {
	rt, _ := testRuntime(t)
	if got := rt.systemPrompt("override"); got != "override" {
		t.Fatalf("got %q", got)
	}
	if got := rt.systemPrompt(""); got == "" {
		t.Fatal("empty system prompt")
	}
	if got := rt.toolGuidance(); got == "" {
		t.Fatal("empty guidance")
	}
	if got := rt.maxRounds(); got != config.DefaultMaxToolRounds {
		t.Fatalf("got %d", got)
	}
	// settings.json overrides.
	settings := `{"system_prompt":"custom sys","tool_guidance":"custom guide","max_tool_rounds":7}`
	if err := os.MkdirAll(filepath.Dir(rt.settingsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.settingsPath(), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := rt.systemPrompt(""); got != "custom sys" {
		t.Fatalf("got %q", got)
	}
	if got := rt.toolGuidance(); got != "custom guide" {
		t.Fatalf("got %q", got)
	}
	if got := rt.maxRounds(); got != 7 {
		t.Fatalf("got %d", got)
	}
	// Corrupt settings fall back to defaults.
	if err := os.WriteFile(rt.settingsPath(), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := rt.systemPrompt(""); got == "" || got == "custom sys" {
		t.Fatalf("got %q", got)
	}
	// Config-level max rounds respected when no settings file.
	rt2, _ := testRuntime(t)
	rt2.config.MaxToolRounds = 12
	if got := rt2.maxRounds(); got != 12 {
		t.Fatalf("got %d", got)
	}
	rt2.config.MaxToolRounds = 9999
	if got := rt2.maxRounds(); got != config.MaxMaxToolRounds {
		t.Fatalf("got %d", got)
	}
}

func TestWorkspaceSettings(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	got, err := rt.SetWorkspace(ws)
	if err != nil || got != ws {
		t.Fatalf("got %q %v", got, err)
	}
	if rt.GetWorkspace() != ws {
		t.Fatalf("got %q", rt.GetWorkspace())
	}
	cleared, err := rt.SetWorkspace("")
	if err != nil || cleared != "" {
		t.Fatalf("got %q %v", cleared, err)
	}
	if _, err := rt.SetWorkspace(filepath.Join(ws, "missing")); err == nil {
		t.Fatal("missing workspace accepted")
	}
	if _, err := rt.SetWorkspace("/tmp/file-not-dir-test"); err == nil {
		// Only fails if the path exists as a file; create it to be sure.
		f := filepath.Join(ws, "f")
		if werr := os.WriteFile(f, []byte("x"), 0o644); werr != nil {
			t.Fatal(werr)
		}
		if _, err2 := rt.SetWorkspace(f); err2 == nil {
			t.Fatal("file workspace accepted")
		}
	}
	// Relative paths resolve against cwd.
	cwd, _ := os.Getwd()
	rel := "."
	abs, err := rt.SetWorkspace(rel)
	if err != nil || abs != cwd && !strings.HasPrefix(abs, "/") {
		t.Fatalf("got %q %v", abs, err)
	}
}

func TestFsListAndHome(t *testing.T) {
	rt, _ := testRuntime(t)
	if rt.FsHome() == "" {
		t.Fatal("empty home")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := rt.FsList(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Directories) != 2 || list.Directories[0].Name != "adir" || list.Directories[1].Name != "bdir" {
		t.Fatalf("not sorted/filtered: %+v", list)
	}
	if list.ParentPath == "" || list.CurrentPath != dir {
		t.Fatalf("got %+v", list)
	}
	// Root parent is "".
	root, err := rt.FsList("/")
	if err != nil {
		t.Fatal(err)
	}
	if root.ParentPath != "" {
		t.Fatalf("got %+v", root)
	}
	if _, err := rt.FsList(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing accepted")
	}
	// Relative resolves against cwd.
	rel, err := rt.FsList(".")
	if err != nil || rel.CurrentPath == "" {
		t.Fatalf("got %+v %v", rel, err)
	}
	// cleanAbs lexical handling.
	if got := cleanAbs("/a/./b/../c"); got != "/a/c" {
		t.Fatalf("got %q", got)
	}
	if got := cleanAbs("/"); got != "/" {
		t.Fatalf("got %q", got)
	}
}

func TestRefreshProvidersDedupAndCatalog(t *testing.T) {
	rt, _ := testRuntime(t)
	if !rt.RefreshProviders() {
		t.Fatal("first refresh should start")
	}
	if rt.RefreshProviders() {
		t.Fatal("second refresh should dedup")
	}
	// refreshCatalog against unreachable hosts leaves models empty.
	// (127.0.0.1:1 refuses connections; the seed URLs would hit the net.)
	store := config.NewStore(t.TempDir(), config.NewMemoryKeyStore())
	seeded, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for i := range seeded.Providers {
		seeded.Providers[i].ApiURL = "http://127.0.0.1:1/"
	}
	if err := store.SaveProviders(seeded); err != nil {
		t.Fatal(err)
	}
	if err := refreshCatalog(store); err != nil {
		t.Fatal(err)
	}
	models, err := store.LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(models.Providers) != 0 {
		t.Fatalf("unreachable hosts should save nothing: %+v", models)
	}
	// Mock catalog server populates models.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [{"id": "zebra"}, {"id": "apple"}]}`)
	}))
	defer srv.Close()
	store2 := config.NewStore(t.TempDir(), config.NewMemoryKeyStore())
	f2, err := store2.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for i := range f2.Providers {
		f2.Providers[i].ApiURL = srv.URL
	}
	if err := store2.SaveProviders(f2); err != nil {
		t.Fatal(err)
	}
	for _, p := range f2.Providers {
		if err := store2.Keys.Set(p.Name, "sk-real"); err != nil {
			t.Fatal(err)
		}
	}
	if err := refreshCatalog(store2); err != nil {
		t.Fatal(err)
	}
	m2, err := store2.LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f2.Providers {
		entry := m2.Providers[p.Name]
		if len(entry.AllModels) != 2 || entry.AllModels[0] != "apple" || entry.AllModels[1] != "zebra" {
			t.Fatalf("not sorted: %+v", entry)
		}
		if len(entry.EnabledModels) != 2 {
			t.Fatalf("enabled not seeded: %+v", entry)
		}
	}
}

func TestTrackerStopSemantics(t *testing.T) {
	tr := newTracker()
	if tr.stop("missing", "") {
		t.Fatal("false positive")
	}
	tr.track("a", "/x", func() {})
	tr.track("b", "/y", func() {})
	if !tr.stop("", "/x") {
		t.Fatal("cwd stop failed")
	}
	if !tr.stop("b", "") {
		t.Fatal("id stop failed")
	}
	// Stop cancels without removing entries (matches the Rust core), so a
	// fresh tracker is what reports idle.
	if newTracker().stop("", "") {
		t.Fatal("fresh tracker should be idle")
	}
	if newTracker().stop("missing", "") {
		t.Fatal("fresh tracker false positive")
	}
}

func TestToolDefinitionsMirrorRegistry(t *testing.T) {
	rt, _ := testRuntime(t)
	defs := rt.tools.Definitions()
	if len(defs) != 4 {
		t.Fatalf("got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"read", "write", "edit", "shell"} {
		if !names[want] {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestEmitDoneNonBlocking(t *testing.T) {
	rt, _ := testRuntime(t)
	tx := make(chan agent.ChatChunk)        // unbuffered, no receiver.
	rt.emitDone(tx, "s", false, false, nil) // must not block.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = ctx
}
