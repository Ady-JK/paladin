package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/tools"
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

func TestNewMessageDropsFinishedPlan(t *testing.T) {
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
	sess, err := rt.OpenSession("", "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	writeTodos := func(payload string) {
		loaded, err := rt.transcripts.Load("/tmp/p", sess.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(loaded.Dir, session.TodoFileName), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	readTodos := func() []session.TodoEntry {
		todos, err := rt.SessionTodos(sess.SessionID, "/tmp/p")
		if err != nil {
			t.Fatal(err)
		}
		return todos
	}

	// A fully-landed plan is dropped by the user's next message.
	writeTodos(`{"todos":[{"content":"One","status":"finished"},{"content":"Two","status":"cancelled"}]}`)
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "next", Cwd: "/tmp/p"})
	if got := readTodos(); len(got) != 0 {
		t.Fatalf("finished plan survived: %+v", got)
	}

	// An unfinished plan survives the next message.
	writeTodos(`{"todos":[{"content":"One","status":"finished"},{"content":"Two","status":"in_progress"}]}`)
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "more", Cwd: "/tmp/p"})
	if got := readTodos(); len(got) != 2 {
		t.Fatalf("unfinished plan dropped: %+v", got)
	}

	// Resume never drops, even when everything landed.
	writeTodos(`{"todos":[{"content":"One","status":"finished"}]}`)
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Resume: true, Cwd: "/tmp/p"})
	if got := readTodos(); len(got) != 1 {
		t.Fatalf("resume dropped the plan: %+v", got)
	}

	// A corrupt file is left alone for the todo tool to report.
	writeTodos(`{broken`)
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "again", Cwd: "/tmp/p"})
	loaded, err := rt.transcripts.Load("/tmp/p", sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(loaded.Dir, session.TodoFileName)); err != nil {
		t.Fatalf("corrupt plan removed: %v", err)
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

func TestResolveProviderDefaultModel(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("Opencode Go", "sk-live"); err != nil {
		t.Fatal(err)
	}
	if err := rt.store.SaveModels(config.ModelsFile{Providers: map[string]config.ProviderModels{
		"Opencode Go": {
			AllModels:     []string{"glm-5.3-flash", "deepseek-v4.1-flash"},
			EnabledModels: []string{"glm-5.3-flash", "deepseek-v4.1-flash"},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	_, _, p, err := rt.resolveProvider("Opencode Go", "")
	if err != nil {
		t.Fatal(err)
	}
	// Prefer the known chat model over alphabetical/catalog order so a
	// new session always has a model the OpenAI dialect can serve.
	if p.Model() != config.OpencodeDefaultModel {
		t.Fatalf("got %q", p.Model())
	}
	_, _, override, err := rt.resolveProvider("Opencode Go", "glm-5.3-flash")
	if err != nil || override.Model() != "glm-5.3-flash" {
		t.Fatalf("override lost: %q %v", override.Model(), err)
	}
}

func TestResolveProviderDefaultModelWithoutCatalog(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("Opencode Go", "sk-live"); err != nil {
		t.Fatal(err)
	}
	_, _, p, err := rt.resolveProvider("", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Model() != config.OpencodeDefaultModel {
		t.Fatalf("got %q", p.Model())
	}
}

func TestResolveProviderCatalogDefaultForPlainProvider(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-or"); err != nil {
		t.Fatal(err)
	}
	if err := rt.store.Keys.Delete("Opencode Go"); err != nil {
		t.Fatal(err)
	}
	if err := rt.store.SaveModels(config.ModelsFile{Providers: map[string]config.ProviderModels{
		"openrouter": {EnabledModels: []string{"vendor/model-a", "vendor/model-b"}},
	}}); err != nil {
		t.Fatal(err)
	}
	_, _, p, err := rt.resolveProvider("", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Model() != "vendor/model-a" {
		t.Fatalf("got %q", p.Model())
	}
}

func TestSystemPromptFileMaxRounds(t *testing.T) {
	rt, dir := testRuntime(t)
	if got := rt.systemPrompt("override"); got != "override" {
		t.Fatalf("got %q", got)
	}
	// First read seeds the file from the built-in default.
	if got := rt.systemPrompt(""); got != config.DefaultSystemPrompt() {
		t.Fatalf("system prompt not seeded from default")
	}
	if _, err := os.Stat(filepath.Join(dir, "config", config.SystemPromptFileName)); err != nil {
		t.Fatalf("system prompt file not seeded: %v", err)
	}
	if got := rt.maxRounds(); got != config.DefaultMaxToolRounds {
		t.Fatalf("got %d", got)
	}
	// The edited file is authoritative.
	edited, err := rt.SetPrompt("custom sys")
	if err != nil {
		t.Fatal(err)
	}
	if edited.SystemPrompt != "custom sys" {
		t.Fatalf("got %+v", edited)
	}
	if got := rt.systemPrompt(""); got != "custom sys" {
		t.Fatalf("got %q", got)
	}
	// Per-request override still beats the file.
	if got := rt.systemPrompt("override"); got != "override" {
		t.Fatalf("got %q", got)
	}
	// A blank file reseeds from the default instead of breaking turns.
	if err := os.WriteFile(rt.store.SystemPromptPath(), []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := rt.systemPrompt(""); got != config.DefaultSystemPrompt() {
		t.Fatalf("blank file not reseeded")
	}
	// Empty saves are rejected.
	if _, err := rt.SetPrompt(""); err == nil {
		t.Fatal("empty set accepted")
	}
	if _, err := rt.SetPrompt("   "); err == nil {
		t.Fatal("blank set accepted")
	}
	if err := rt.store.SaveSystemPrompt(""); err == nil {
		t.Fatal("empty save accepted")
	}
	// Reset restores the built-in default.
	if _, err := rt.SetPrompt("custom sys"); err != nil {
		t.Fatal(err)
	}
	reset, err := rt.ResetPrompt()
	if err != nil {
		t.Fatal(err)
	}
	if reset.SystemPrompt != config.DefaultSystemPrompt() {
		t.Fatalf("reset did not restore default")
	}
	// settings.json max rounds still apply; corrupt files fall back.
	settings := `{"max_tool_rounds":7}`
	if err := os.MkdirAll(filepath.Dir(rt.settingsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.settingsPath(), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := rt.maxRounds(); got != 7 {
		t.Fatalf("got %d", got)
	}
	if err := os.WriteFile(rt.settingsPath(), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := rt.maxRounds(); got != config.DefaultMaxToolRounds {
		t.Fatalf("got %d", got)
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
	if len(defs) != 6 {
		t.Fatalf("got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"read", "write", "edit", "shell", "ask", "todo"} {
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

// TestChatRunsEveryBuiltinTool drives write, read, edit and shell through
// the real runtime.Chat -> agent template -> model stream -> tools path,
// then checks both the streamed tool results and the file on disk.
func TestChatRunsEveryBuiltinTool(t *testing.T) {
	wsDir := t.TempDir()
	toolRound := func(id, name, args string) string {
		return fmt.Sprintf(
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%q,"function":{"name":%q,"arguments":%q}}]}}]}`,
			id, name, args)
	}
	rounds := []string{
		toolRound("c1", "write", `{"path":"`+filepath.Join(wsDir, "notes.txt")+`","content":"alpha\n"}`),
		toolRound("c2", "read", `{"path":"`+filepath.Join(wsDir, "notes.txt")+`"}`),
		toolRound("c3", "edit", `{"path":"`+filepath.Join(wsDir, "notes.txt")+`","old_string":"alpha","new_string":"beta"}`),
		toolRound("c4", "shell", `{"cmd":"cat notes.txt"}`),
	}
	var calls int64
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n < int64(len(rounds)) {
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", rounds[n])
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"all done\"}}]}\n\ndata: [DONE]\n\n")
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
	outcome, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "do it", Cwd: wsDir})
	if outcome.Reply != "all done" {
		t.Fatalf("got %+v", outcome)
	}

	results := map[string]string{}
	for _, c := range chunks {
		if c.Tool != nil && c.Tool.Status == "ok" {
			results[c.Tool.Name] = c.Tool.Result
		}
	}
	for _, name := range []string{"write", "read", "edit", "shell"} {
		if _, ok := results[name]; !ok {
			t.Fatalf("no successful %s result in %+v", name, chunks)
		}
	}
	if !strings.Contains(results["write"], `"created":true`) {
		t.Fatalf("write result: %s", results["write"])
	}
	if !strings.Contains(results["read"], "alpha") {
		t.Fatalf("read result: %s", results["read"])
	}
	if !strings.Contains(results["edit"], `"replacements":1`) {
		t.Fatalf("edit result: %s", results["edit"])
	}
	if !strings.Contains(results["shell"], "beta") {
		t.Fatalf("shell result: %s", results["shell"])
	}

	data, err := os.ReadFile(filepath.Join(wsDir, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "beta\n" {
		t.Fatalf("file = %q", data)
	}

	// user, 4 x (assistant call + tool result), final assistant text.
	history, err := rt.SessionHistory(sess.SessionID, wsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 10 {
		t.Fatalf("got %d rows: %+v", len(history), history)
	}
}

func TestChatAskRoundTripBlocksUntilAnswered(t *testing.T) {
	wsDir := t.TempDir()
	askRound := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c-ask-1","function":{"name":"ask","arguments":"{\"questions\":[{\"question\":\"Deploy?\",\"options\":[\"yes\",\"no\"]}]}"}}]}}]}`
	var calls int64
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		mu.Lock()
		n := calls
		calls++
		mu.Unlock()
		if n == 0 {
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", askRound)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"deploying yes\"}}]}\n\ndata: [DONE]\n\n")
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
	done := make(chan struct {
		outcome agent.ChatOutcome
		chunks  []agent.ChatChunk
	}, 1)
	go func() {
		outcome, chunks := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "ship it", Cwd: wsDir})
		done <- struct {
			outcome agent.ChatOutcome
			chunks  []agent.ChatChunk
		}{outcome: outcome, chunks: chunks}
	}()
	// The turn blocks inside the ask call until the UI answers.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if rt.ask.Pending(sess.SessionID, "c-ask-1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ask call never blocked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Unknown calls fail so the UI can report a moved-on turn.
	if _, err := rt.AnswerAsk(sess.SessionID, "nope", nil); err == nil {
		t.Fatal("answer of unknown call passed")
	}
	ok, err := rt.AnswerAsk(sess.SessionID, "c-ask-1", []tools.AskAnswer{{Question: "Deploy?", Answer: "yes"}})
	if err != nil || !ok {
		t.Fatalf("answer: %v %v", ok, err)
	}
	var res struct {
		outcome agent.ChatOutcome
		chunks  []agent.ChatChunk
	}
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("answered turn never continued")
	}
	if res.outcome.Reply != "deploying yes" {
		t.Fatalf("got %+v", res.outcome)
	}
	var askResult string
	var runningSeen bool
	for _, c := range res.chunks {
		if c.Tool == nil || c.Tool.Name != "ask" {
			continue
		}
		if c.Tool.Status == session.ToolRunning {
			runningSeen = true
		}
		if c.Tool.Status == session.ToolOK {
			askResult = c.Tool.Result
		}
	}
	if !runningSeen {
		t.Fatal("no running ask event")
	}
	if !strings.Contains(askResult, `"answer":"yes"`) || !strings.Contains(askResult, `"question":"Deploy?"`) {
		t.Fatalf("ask result: %s", askResult)
	}
}

func TestCancelAskStopsTurn(t *testing.T) {
	wsDir := t.TempDir()
	askRound := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c-ask-9","function":{"name":"ask","arguments":"{\"questions\":[{\"question\":\"Go?\"}]}"}}]}}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", askRound)
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
	type res struct {
		outcome agent.ChatOutcome
		err     error
	}
	done := make(chan res, 1)
	tx := make(chan agent.ChatChunk, 256)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		outcome, err := rt.Chat(agent.Request{SessionID: sess.SessionID, Message: "go", Cwd: wsDir}, tx, stop)
		done <- res{outcome: outcome, err: err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if rt.ask.Pending(sess.SessionID, "c-ask-9") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ask call never blocked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !rt.CancelAsk(sess.SessionID, "c-ask-9") {
		t.Fatal("cancel missed the pending ask")
	}
	if rt.CancelAsk(sess.SessionID, "c-ask-9") {
		t.Fatal("second cancel should report nothing waiting")
	}
	var r res
	select {
	case r = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled turn never returned")
	}
	if r.err != nil {
		t.Fatalf("cancel should stop cleanly, got %v", r.err)
	}
	if !r.outcome.Stopped {
		t.Fatalf("got %+v", r.outcome)
	}
}

func TestNewHonoursMemoryKeyStoreOverride(t *testing.T) {
	t.Setenv("PALADIN_KEYSTORE", "  MEMORY  ")
	dir := t.TempDir()
	rt := New(dir, config.NewConfig(dir))
	if err := rt.store.Keys.Set("provider-x", "sk-test"); err != nil {
		t.Fatal(err)
	}
	key, ok, err := rt.store.Keys.Get("provider-x")
	if err != nil || !ok || key != "sk-test" {
		t.Fatalf("got %q %v %v", key, ok, err)
	}
}

func TestSessionUsageAfterBilledTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3,\"total_tokens\":14,\"cost\":0.001,\"opencode\":{\"list_cost\":0.002}}}\n\ndata: [DONE]\n\n")
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
	outcome, _ := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: "/tmp/proj"})
	if outcome.Usage.TotalTokens != 14 {
		t.Fatalf("turn usage %+v", outcome.Usage)
	}
	info, err := rt.SessionUsage(sess.SessionID, "/tmp/proj")
	if err != nil {
		t.Fatal(err)
	}
	if info.Calls != 1 || info.TotalTokens != 14 || info.PromptTokens != 11 || info.CompletionTokens != 3 {
		t.Fatalf("got %+v", info)
	}
	if !info.HasCost || info.Cost != 0.001 || info.ListCost != 0.002 {
		t.Fatalf("cost %+v", info)
	}
	// The default mock provider has no model name, so the window is
	// unknown and no fill percentage is reported.
	if info.ContextKnown || info.FillPercent != nil {
		t.Fatalf("got %+v", info)
	}
	if _, err := rt.SessionUsage("", "/tmp/proj"); err == nil {
		t.Fatal("empty id accepted")
	}
	if _, err := rt.SessionUsage("00000000-0000-4000-8000-000000000099", "/tmp/proj"); err == nil {
		t.Fatal("unknown session accepted")
	}
}

func TestSessionUsageKnownWindow(t *testing.T) {
	rt, _ := testRuntime(t)
	sess, err := rt.transcripts.Create("/tmp/p", "openrouter", "kimi-k2.6")
	if err != nil {
		t.Fatal(err)
	}
	var totals session.UsageTotals
	totals.AddCall("kimi-k2.6", 1000, 1000, 0, 0, 0, 2000, 0.05, 0.1, true, 1)
	data, err := json.Marshal(totals)
	if err != nil {
		t.Fatal(err)
	}
	usagePath := sess.Dir + "/usage.json"
	if err := os.WriteFile(usagePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := rt.SessionUsage(sess.Meta.SessionID, "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	if !info.ContextKnown || info.ContextWindow != 262144 || info.Model != "kimi-k2.6" {
		t.Fatalf("got %+v", info)
	}
	if info.FillPercent == nil {
		t.Fatal("no fill percent")
	}
	// Fill reads the last call's 1000 input tokens, not the 2000 total.
	want := float64(1000) / float64(262144) * 100
	if *info.FillPercent != want {
		t.Fatalf("got %v want %v", *info.FillPercent, want)
	}
	// A session with no ledger reports zeros but still resolves the
	// window from the transcript model.
	bare, err := rt.transcripts.Create("/tmp/p", "openrouter", "kimi-k2.6")
	if err != nil {
		t.Fatal(err)
	}
	bareInfo, err := rt.SessionUsage(bare.Meta.SessionID, "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	if bareInfo.Calls != 0 || bareInfo.TotalTokens != 0 || bareInfo.HasCost {
		t.Fatalf("got %+v", bareInfo)
	}
	if !bareInfo.ContextKnown || bareInfo.FillPercent == nil || *bareInfo.FillPercent != 0 {
		t.Fatalf("got %+v", bareInfo)
	}
}

func TestOverallUsageAggregates(t *testing.T) {
	rt, _ := testRuntime(t)
	a, err := rt.transcripts.Create("/tmp/p", "opencode-go", "beam-free")
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.transcripts.Create("/tmp/p", "opencode-go", "kimi-k2.6")
	if err != nil {
		t.Fatal(err)
	}
	var ta session.UsageTotals
	ta.AddCall("beam-free", 100, 50, 0, 10, 0, 150, 0.01, 0.02, true, 1790966400)
	var tb session.UsageTotals
	tb.AddCall("kimi-k2.6", 200, 100, 5, 0, 5, 300, 0.02, 0.04, true, 1790966400)
	for _, tc := range []struct {
		sess   string
		totals session.UsageTotals
	}{
		{a.Dir, ta},
		{b.Dir, tb},
	} {
		data, _ := json.Marshal(tc.totals)
		if err := os.WriteFile(tc.sess+"/usage.json", data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	overall, err := rt.OverallUsage("/tmp/p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if overall.SessionCount != 2 || overall.SessionsWithUsage != 2 {
		t.Fatalf("got %+v", overall)
	}
	if overall.TotalTokens != 450 || overall.Calls != 2 {
		t.Fatalf("got %+v", overall)
	}
	if len(overall.Models) != 2 || overall.Models[0].Model != "kimi-k2.6" {
		t.Fatalf("models %+v", overall.Models)
	}
	if len(overall.Sessions) != 2 || len(overall.Daily) != 1 {
		t.Fatalf("sessions/daily %+v %+v", overall.Sessions, overall.Daily)
	}
	// Limit caps the session table but not the totals.
	limited, err := rt.OverallUsage("/tmp/p", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Sessions) != 1 || limited.TotalTokens != 450 {
		t.Fatalf("got %+v", limited)
	}
}

func TestFileCompleteMentions(t *testing.T) {
	rt, _ := testRuntime(t)
	ws := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/main.go", "package main\n")
	write("src/server/app.go", "package server\n")
	write(".gitignore", "ignored/\n")
	write("ignored/x.go", "package x\n")
	write("notes/todo.txt", "todo\n")
	sib := filepath.Join(filepath.Dir(ws), "sibling")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sib, "outside.txt"), []byte("out\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force the walk fallback (hermetic: no rg, no git needed).
	t.Setenv("PATH", "")
	got, err := rt.FileComplete("src/ma", ws, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Path != "src/main.go" {
		t.Fatalf("got %+v", got)
	}
	if got[0].ResolvedPath != filepath.Join(ws, "src", "main.go") {
		t.Fatalf("got %+v", got[0])
	}
	got, err = rt.FileComplete("", ws, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if strings.Contains(e.Path, "ignored/") {
			t.Fatalf("gitignored suggested: %+v", e)
		}
	}
	got, err = rt.FileComplete("../sib", ws, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range got {
		if e.Path == "../sibling/outside.txt" && e.Outside && filepath.IsAbs(e.ResolvedPath) {
			found = true
		}
	}
	if !found {
		t.Fatalf("outside file missing: %+v", got)
	}
	if _, err := rt.FileComplete("x", "", 0); err == nil {
		t.Fatal("empty workspace accepted")
	}
}

func TestListSkills(t *testing.T) {
	rt, dir := testRuntime(t)
	ws := t.TempDir()
	write := func(root, name, content string) {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "config", "skills"), "reviewer", "---\ndescription: Reviews code\n---\n# R")
	write(filepath.Join(ws, ".paladin", "skills"), "deployer", "# Deploy")

	// The workspace cwd merges both roots.
	got := rt.ListSkills(ws)
	if len(got) != 2 || got[0].Name != "deployer" || got[1].Name != "reviewer" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Description != "Reviews code" || got[1].Source != "global" {
		t.Fatalf("got %+v", got[1])
	}
	if got[0].Source != "workspace" {
		t.Fatalf("got %+v", got[0])
	}
	// Empty cwd still lists global skills.
	if got := rt.ListSkills(""); len(got) != 1 || got[0].Name != "reviewer" {
		t.Fatalf("got %+v", got)
	}
}

func TestChatInjectsSkillContent(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &req)
		for _, m := range req.Messages {
			if m.Role == "user" {
				bodies = append(bodies, m.Content)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "done"}}]}`)
	}))
	defer server.Close()
	rt, dir := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	pointMockAt(rt, server.URL)
	ws := t.TempDir()
	d := filepath.Join(dir, "config", "skills", "reviewer")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("review guidelines"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	skill := "reviewer"
	outcome, _ := chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "check this", Cwd: ws, Skill: &skill})
	if outcome.Reply != "done" {
		t.Fatalf("got %+v", outcome)
	}
	if len(bodies) == 0 {
		t.Fatal("no user message reached the mock provider")
	}
	last := bodies[len(bodies)-1]
	if !strings.Contains(last, "check this") ||
		!strings.Contains(last, "Use this skill (reviewer):") ||
		!strings.Contains(last, "review guidelines") {
		t.Fatalf("skill not injected: %q", last)
	}
	// The persisted transcript keeps the expansion, not the short text.
	history, err := rt.SessionHistory(sess.SessionID, ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 || !strings.Contains(history[0].Content, "Use this skill (reviewer):") {
		t.Fatalf("transcript missing expansion: %+v", history)
	}

	// Unknown skills fail the turn instead of dropping content.
	bad := "nope"
	tx := make(chan agent.ChatChunk, 16)
	done := make(chan struct{})
	defer close(done)
	if _, err := rt.Chat(agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: ws, Skill: &bad}, tx, done); err == nil {
		t.Fatal("unknown skill accepted")
	}
}
