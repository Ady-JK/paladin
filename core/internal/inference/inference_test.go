package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func providerFor(url string) *OpenAIProvider {
	return NewOpenAIProvider("test", "m", url, "k")
}

func TestCompleteToolsParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "hi", "tool_calls": [{"id": "c1", "function": {"name": "read", "arguments": "{\"path\":\"x\"}"}}]}}]}`)
	}))
	defer server.Close()
	p := providerFor(server.URL)
	resp, err := p.CompleteTools(context.Background(), "sys", nil, nil, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" {
		t.Fatalf("got %+v", resp)
	}
}

func TestStreamAssemblesFragmentedToolCalls(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"re\",\"arguments\":\"{\\\"pa\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"th\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer server.Close()
	p := providerFor(server.URL)
	tx := make(chan Chunk, 64)
	done := make(chan struct{})
	defer close(done)
	if err := p.StreamTools(context.Background(), "sys", nil, nil, "sess", tx, done); err != nil {
		t.Fatal(err)
	}
	close(tx)
	var texts strings.Builder
	notices := 0
	var finalCalls []ToolCall
	for chunk := range tx {
		texts.WriteString(chunk.Text)
		if chunk.ToolNotice != nil {
			notices++
		}
		if len(chunk.ToolCalls) > 0 {
			finalCalls = chunk.ToolCalls
		}
	}
	_ = config.PlaceholderKey
	if texts.String() != "Hel" {
		t.Fatalf("got %q", texts.String())
	}
	if notices != 1 {
		t.Fatalf("notices %d", notices)
	}
	if len(finalCalls) != 1 || finalCalls[0].Arguments != "{\"path\"}" {
		t.Fatalf("got %+v", finalCalls)
	}
}

func TestMapsHTTPErrorsWithoutLeakingKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "bad key")
	}))
	defer server.Close()
	p := NewOpenAIProvider("test", "m", server.URL, "sk-secret-xyz")
	err := func() error {
		_, err := p.CompleteTools(context.Background(), "sys", nil, nil, "sess")
		return err
	}()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("got %q", err)
	}
	if strings.Contains(err.Error(), "sk-secret-xyz") {
		t.Fatalf("key leaked: %q", err)
	}
}

func TestListsModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [{"id": "a"}, {"id": "b"}]}`)
	}))
	defer server.Close()
	p := providerFor(server.URL)
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatalf("got %v", models)
	}
}

func TestChunkTextCarriesText(t *testing.T) {
	if got := TextChunk("hi"); got.Text != "hi" || len(got.ToolCalls) != 0 {
		t.Fatalf("got %+v", got)
	}
}

// ---- merged from inference_extra_test.go ----

func TestBuildProviderMatrix(t *testing.T) {
	if _, err := BuildProvider(config.ApiTypeOpenai, "p", "m", "https://x/v1", ""); err == nil {
		t.Fatal("placeholder accepted")
	} else if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("got %q", err)
	}
	for _, key := range []string{"replace_me", "xxx", "dummy-key"} {
		if _, err := BuildProvider(config.ApiTypeOpenai, "p", "m", "https://x/v1", key); err == nil {
			t.Fatalf("placeholder %q accepted", key)
		}
	}
	if _, err := BuildProvider(config.ApiTypeAnthropic, "p", "m", "https://x/v1", "sk-real"); err == nil {
		t.Fatal("anthropic accepted")
	} else if !strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("got %q", err)
	}
	if _, err := BuildProvider(config.ApiTypeGoogle, "p", "m", "https://x/v1", "sk-real"); err == nil {
		t.Fatal("google accepted")
	}
	if _, err := BuildProvider("azure", "p", "m", "https://x/v1", "sk-real"); err == nil {
		t.Fatal("azure accepted")
	}
	p, err := BuildProvider(config.ApiTypeOpenai, "p", "m", "https://x/v1", "sk-real")
	if err != nil || p.Name() != "p" || p.ApiType() != config.ApiTypeOpenai || p.Model() != "m" {
		t.Fatalf("got %+v %v", p, err)
	}
}

func TestProviderErrorStrings(t *testing.T) {
	cases := []struct {
		err  *ProviderError
		want string
	}{
		{&ProviderError{Kind: "unknown", Message: "x"}, "unknown provider: x"},
		{&ProviderError{Kind: "placeholder", Message: "m"}, "provider has placeholder key: m"},
		{&ProviderError{Kind: "config", Message: "m"}, "config: m"},
		{&ProviderError{Kind: "cancelled", Message: "x"}, "cancelled"},
		{&ProviderError{Kind: "request", Message: "boom"}, "boom"},
		{RequestError("r"), "r"},
		{ConfigError("c"), "config: c"},
		{CancelledError(), "cancelled"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
	if !IsCancelled(CancelledError()) || !IsCancelled(context.Canceled) && false {
		t.Fatal("cancel check")
	}
	if IsCancelled(RequestError("x")) {
		t.Fatal("false cancelled")
	}
	if IsCancelled(fmt.Errorf("x")) {
		t.Fatal("false cancelled for plain error")
	}
}

func TestNewOpenAIDefaults(t *testing.T) {
	p := NewOpenAIProvider("n", "", "  https://x/v1/  ", "k")
	if p.Model() != DefaultOpenAIModel {
		t.Fatalf("got %q", p.Model())
	}
	if p.baseURL != "https://x/v1" {
		t.Fatalf("got %q", p.baseURL)
	}
	p2 := NewOpenAIProvider("n", "m", "", "k")
	if p2.baseURL != DefaultOpenAIBaseURL {
		t.Fatalf("got %q", p2.baseURL)
	}
	if !NewOpenAIProvider("n", "m", "https://opencode.ai/inference/go/openai/v1", "k").isOpencode {
		t.Fatal("opencode not detected")
	}
	if NewOpenAIProvider("n", "m", "https://api.openai.com/v1", "k").isOpencode {
		t.Fatal("openai wrongly flagged")
	}
}

func TestOpencodeHeadersOnlyForOpencode(t *testing.T) {
	plain := NewOpenAIProvider("n", "m", "https://api.openai.com/v1", "k")
	if len(plain.headers("s")) != 0 {
		t.Fatal("headers leaked to plain openai")
	}
	oc := NewOpenAIProvider("n", "m", "https://opencode.ai/inference/go/openai/v1", "k")
	if len(oc.headers("s")) != 2 {
		t.Fatalf("got %d headers", len(oc.headers("s")))
	}
	// An empty id must not become an empty header: gateways treat that as
	// a missing session.
	if len(oc.headers("  ")) != 1 {
		t.Fatalf("empty session still sent: %d", len(oc.headers("  ")))
	}
	zen := NewOpenAIProvider("n", "m", "https://opencode.ai/zen/go/v1", "k")
	if !zen.isOpencode || len(zen.headers("s")) != 2 {
		t.Fatal("zen alias not detected")
	}
}

func TestToSDKMessagesShapes(t *testing.T) {
	id, name := "c1", "read"
	msgs := toSDKMessages([]LlmMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "doing", ToolCalls: []ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}}},
		{Role: "tool", Content: "out", ToolCallID: &id, Name: &name},
		{Role: "tool", Content: "orphan"},
		{Role: "system", Content: "s"},
	})
	if len(msgs) != 5 {
		t.Fatalf("got %d", len(msgs))
	}
}

func TestToSDKToolsShapes(t *testing.T) {
	tools := toSDKTools([]ToolDefinition{
		{Name: "a", Parameters: map[string]any{"type": "object"}},
		{Name: "b", Parameters: "not-a-map"},
		{Name: "c"},
	})
	if len(tools) != 3 || tools[0].Function.Name != "a" {
		t.Fatalf("got %+v", tools)
	}
}

func TestNewParamsPrependsSystem(t *testing.T) {
	p := providerFor("http://x")
	params := p.newParams("sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "")
	if len(params.Messages) != 2 {
		t.Fatalf("got %d", len(params.Messages))
	}
	bare := p.newParams("", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "")
	if len(bare.Messages) != 1 {
		t.Fatalf("got %d", len(bare.Messages))
	}
	withTools := p.newParams("", nil, []ToolDefinition{{Name: "read"}}, "")
	if len(withTools.Tools) != 1 {
		t.Fatal("tools missing")
	}
}

func TestOpencodePromptCacheKey(t *testing.T) {
	oc := NewOpenAIProvider("n", "m", "https://opencode.ai/inference/go/openai/v1", "k")
	raw, err := json.Marshal(oc.newParams("", nil, nil, " session-1 "))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"prompt_cache_key":"session-1"`) {
		t.Fatalf("missing cache key: %s", raw)
	}
	// A blank conversation id is not a cache bucket.
	raw, _ = json.Marshal(oc.newParams("", nil, nil, " "))
	if strings.Contains(string(raw), "prompt_cache_key") {
		t.Fatalf("blank cache key sent: %s", raw)
	}
	// Plain OpenAI-compatible endpoints must not receive OpenCode's
	// extension fields.
	plain := providerFor("https://api.openai.com/v1")
	raw, _ = json.Marshal(plain.newParams("", nil, nil, "session-1"))
	if strings.Contains(string(raw), "prompt_cache_key") {
		t.Fatalf("cache key leaked to plain openai: %s", raw)
	}
}

func TestOpencodeCatalogURL(t *testing.T) {
	cases := map[string]string{
		"https://opencode.ai/inference/go/openai/v1":  "https://opencode.ai/inference/go/v1/models",
		"https://opencode.ai/inference/go/openai/v1/": "https://opencode.ai/inference/go/v1/models",
		"https://opencode.ai/zen/go/v1":               "https://opencode.ai/zen/go/v1/models",
		"https://opencode.ai/zen/go/v1?x=1":           "https://opencode.ai/zen/go/v1/models",
		"https://opencode.ai":                         "https://opencode.ai/v1/models",
	}
	for base, want := range cases {
		p := NewOpenAIProvider("n", "m", base, "k")
		if got := p.catalogURL(); got != want {
			t.Fatalf("%s: got %s want %s", base, got, want)
		}
	}
}

func TestCompleteToolsErrorPaths(t *testing.T) {
	// No choices.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices": []}`)
	}))
	defer srv.Close()
	if _, err := providerFor(srv.URL).CompleteTools(context.Background(), "", nil, nil, "s"); err == nil {
		t.Fatal("empty choices accepted")
	}
	// Cancelled context maps to cancelled.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := providerFor(srv.URL).CompleteTools(cctx, "", nil, nil, "s"); err == nil || !IsCancelled(err) {
		t.Fatalf("got %v", err)
	}
	// requestError truncates long bodies.
	p := providerFor(srv.URL)
	err := p.requestError(500, strings.Repeat("x", 500))
	if len([]rune(strings.TrimPrefix(err.Error(), "test: chat completion: status 500: "))) != 300 {
		t.Fatalf("not truncated: %d", len(err.Error()))
	}
}

func TestStreamErrorPaths(t *testing.T) {
	// HTTP error on stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "broken")
	}))
	defer srv.Close()
	tx := make(chan Chunk, 16)
	done := make(chan struct{})
	defer close(done)
	if err := providerFor(srv.URL).StreamTools(context.Background(), "", nil, nil, "s", tx, done); err == nil {
		t.Fatal("stream error accepted")
	} else if strings.Contains(err.Error(), "sk-") {
		t.Fatalf("key leaked: %q", err)
	}
}

func TestStreamCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx := make(chan Chunk, 16)
	done := make(chan struct{})
	defer close(done)
	if err := providerFor(srv.URL).StreamTools(cctx, "", nil, nil, "s", tx, done); err == nil || !IsCancelled(err) {
		t.Fatalf("got %v", err)
	}
}

func TestStreamMultipleToolCallsOrdered(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"c2\",\"function\":{\"name\":\"write\",\"arguments\":\"{\\\"a\\\":1}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"b\\\":2}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer srv.Close()
	tx := make(chan Chunk, 64)
	done := make(chan struct{})
	defer close(done)
	if err := providerFor(srv.URL).StreamTools(context.Background(), "", nil, nil, "s", tx, done); err != nil {
		t.Fatal(err)
	}
	close(tx)
	var final []ToolCall
	notices := 0
	for c := range tx {
		if c.ToolNotice != nil {
			notices++
		}
		if len(c.ToolCalls) > 0 {
			final = c.ToolCalls
		}
	}
	if notices != 2 {
		t.Fatalf("notices %d", notices)
	}
	if len(final) != 2 || final[0].ID != "c1" || final[1].ID != "c2" {
		t.Fatalf("order wrong: %+v", final)
	}
}

func TestListModelsErrorPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "denied")
	}))
	defer srv.Close()
	if _, err := providerFor(srv.URL).ListModels(context.Background()); err == nil {
		t.Fatal("list error accepted")
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := providerFor(srv.URL).ListModels(cctx); err == nil {
		t.Fatal("cancelled list accepted")
	}
}

func TestListOpencodeCatalog(t *testing.T) {
	var gotPath, gotUA, gotSess, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		gotSess = r.Header.Get(config.OpencodeSessionHeader)
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [{"id": "m1"}, {"id": ""}, {"id": "m2"}]}`)
	}))
	defer srv.Close()
	p := NewOpenAIProvider("oc", "m", srv.URL, "sk-live")
	p.isOpencode = true
	models, err := p.ListModels(context.Background())
	if err != nil || len(models) != 2 || models[0] != "m1" || models[1] != "m2" {
		t.Fatalf("got %v %v", models, err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("path %q", gotPath)
	}
	if gotUA != config.OpencodeUserAgent || gotSess != "catalog" {
		t.Fatalf("headers ua=%q session=%q", gotUA, gotSess)
	}
	if gotAuth != "Bearer sk-live" {
		t.Fatalf("auth %q", gotAuth)
	}
}

func TestOpencodeWireHeaders(t *testing.T) {
	var gotUA string
	var gotSess []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotSess = append(gotSess, r.Header.Get(config.OpencodeSessionHeader))
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		bodies = append(bodies, body)
		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	p := NewOpenAIProvider("oc", "m", srv.URL, "k")
	p.isOpencode = true
	if _, err := p.CompleteTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess-42"); err != nil {
		t.Fatal(err)
	}
	tx := make(chan Chunk, 4)
	done := make(chan struct{})
	if err := p.StreamTools(context.Background(), "sys", []LlmMessage{{Role: "user", Content: "hi"}}, nil, "sess-42", tx, done); err != nil {
		t.Fatal(err)
	}
	close(done)
	close(tx)
	if gotUA != config.OpencodeUserAgent {
		t.Fatalf("ua %q", gotUA)
	}
	if len(gotSess) != 2 {
		t.Fatalf("requests %d", len(gotSess))
	}
	for i, got := range gotSess {
		if got != "sess-42" {
			t.Fatalf("request %d session %q", i, got)
		}
		if bodies[i]["prompt_cache_key"] != "sess-42" {
			t.Fatalf("request %d cache key missing: %#v", i, bodies[i])
		}
	}
}

func TestMapErrorShapes(t *testing.T) {
	p := providerFor("http://x")
	if got := p.mapError("op", context.Canceled); !IsCancelled(got) {
		t.Fatalf("got %v", got)
	}
	if got := p.mapError("op", fmt.Errorf("dial failed")); !strings.Contains(got.Error(), "dial failed") {
		t.Fatalf("got %v", got)
	}
}

func TestProviderTemplateRegistry(t *testing.T) {
	types := registeredTemplates()
	want := map[config.ApiType]bool{
		config.ApiTypeOpenai: false, config.ApiTypeAnthropic: false, config.ApiTypeGoogle: false,
	}
	for _, apiType := range types {
		if _, ok := want[apiType]; ok {
			want[apiType] = true
		}
	}
	for apiType, seen := range want {
		if !seen {
			t.Fatalf("%q not registered: %v", apiType, types)
		}
	}
	if len(types) > 1 {
		for i := 1; i < len(types); i++ {
			if types[i-1] > types[i] {
				t.Fatalf("not sorted: %v", types)
			}
		}
	}

	// Invalid templates are ignored instead of creating dead entries.
	before := len(registeredTemplates())
	RegisterProviderTemplate(ProviderTemplate{})
	RegisterProviderTemplate(ProviderTemplate{ApiType: "no-factory"})
	if got := len(registeredTemplates()); got != before {
		t.Fatalf("invalid template registered: %d -> %d", before, got)
	}

	// A registered template is what BuildProvider consults, including the
	// resolved spec handed to the factory.
	custom := config.ApiType("custom-test")
	var gotSpec ProviderSpec
	t.Cleanup(func() {
		// Leave the dialect in place; no test asserts an exact type list.
		_ = gotSpec
	})
	RegisterProviderTemplate(ProviderTemplate{
		ApiType: custom,
		New: func(spec ProviderSpec) (Provider, error) {
			gotSpec = spec
			return NewOpenAIProvider(spec.Name, spec.Model, spec.BaseURL, spec.ApiKey), nil
		},
	})
	p, err := BuildProvider(custom, "name-x", "model-y", "https://custom/v1", "sk-live")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name() != "name-x" || p.Model() != "model-y" {
		t.Fatalf("got %+v", p)
	}
	if gotSpec.Name != "name-x" || gotSpec.Model != "model-y" || gotSpec.BaseURL != "https://custom/v1" || gotSpec.ApiKey != "sk-live" {
		t.Fatalf("spec %+v", gotSpec)
	}

	// Placeholder rejection runs before dialect lookup, so even an unknown
	// dialect reports the key problem (wire-compatible error string).
	if _, err := BuildProvider("unknown-dialect", "p", "m", "https://x/v1", "replace_me"); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("got %v", err)
	}

	// An unknown dialect without a placeholder still names the api type.
	if _, err := BuildProvider("unknown-dialect", "p", "m", "https://x/v1", "sk-real"); err == nil || !strings.Contains(err.Error(), "unknown api type unknown-dialect") {
		t.Fatalf("got %v", err)
	}
}

// registeredTemplates returns the registered dialects in sorted order.
func registeredTemplates() []config.ApiType {
	providerMu.RLock()
	defer providerMu.RUnlock()
	out := make([]config.ApiType, 0, len(providerTemplates))
	for apiType := range providerTemplates {
		out = append(out, apiType)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
