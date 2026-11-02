package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// catalogFetcher serves one provider's listing, metadata included, so
// the refresh path and everything downstream can be exercised without
// the network.
func catalogFetcher(fetch CatalogFetch) CatalogFetcher {
	return func(context.Context, config.Provider, string) (CatalogFetch, error) {
		return fetch, nil
	}
}

// deepseek is a chat model whose levels no name rule knows, published
// with its own limits and prices.
func deepseekMeta() config.ModelMeta {
	return config.ModelMeta{
		ContextWindow:  1000000,
		MaxOutput:      384000,
		Protocol:       "chat",
		ThinkingLevels: []string{"low", "high", "max"},
		ThinkingKnown:  true,
		PriceInput:     0.15,
		PriceOutput:    0.6,
		PriceCacheRead: 0.003,
	}
}

// A refresh stores the listing's metadata, and the next ThinkingGet
// reads the levels from it rather than guessing from the model's name.
func TestRefreshStoresMetadataAndDrivesThinkingLevels(t *testing.T) {
	rt, _ := testRuntime(t)
	rt.SetCatalogFetcher(catalogFetcher(CatalogFetch{
		Models: []string{"deepseek-v4.1-flash"},
		Meta:   map[string]config.ModelMeta{"deepseek-v4.1-flash": deepseekMeta()},
	}))
	if _, err := rt.RefreshProvider(config.DefaultProviderName); err != nil {
		t.Fatal(err)
	}

	// The name tells the inference package nothing; the catalog does.
	if levels := inference.ModelEffortLevels(config.ApiTypeOpenai, config.OpencodeBaseURL, "deepseek-v4.1-flash", config.ModelMeta{}); levels != nil {
		t.Fatalf("without metadata the name must yield no levels, got %v", levels)
	}
	v, err := rt.ThinkingGet(config.DefaultProviderName, "deepseek-v4.1-flash")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(v.Levels, ",") != "low,high,max" {
		t.Fatalf("levels %v", v.Levels)
	}
	if _, err := rt.ThinkingSet(config.DefaultProviderName, "deepseek-v4.1-flash", "max"); err != nil {
		t.Fatalf("a published level must be accepted: %v", err)
	}
	if _, err := rt.ThinkingSet(config.DefaultProviderName, "deepseek-v4.1-flash", "xhigh"); err == nil {
		t.Fatal("a level the listing did not publish must be rejected")
	}

	// listProviders carries the metadata for the picker's detail pane.
	infos := rt.ListProviders()
	var meta config.ModelMeta
	for _, p := range infos {
		if p.Name == config.DefaultProviderName {
			meta = p.Meta["deepseek-v4.1-flash"]
		}
	}
	if meta.PriceInput != 0.15 || meta.PriceOutput != 0.6 || meta.ContextWindow != 1000000 {
		t.Fatalf("wire metadata %+v", meta)
	}
}

// A level the catalog published is what a turn actually sends: the
// stored choice reaches the provider the runtime builds for that model.
func TestATurnSendsTheCatalogLevel(t *testing.T) {
	rt, _ := testRuntime(t)
	_ = rt.store.Keys.Set("openrouter", "sk-test")
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	pointMockAt(rt, srv.URL)

	const model = "deepseek-v4.1-flash"
	rt.SetCatalogFetcher(catalogFetcher(CatalogFetch{
		Models: []string{model},
		Meta:   map[string]config.ModelMeta{model: deepseekMeta()},
	}))
	if _, err := rt.RefreshProvider("openrouter"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ThinkingSet("openrouter", model, "max"); err != nil {
		t.Fatalf("a published level must be storable: %v", err)
	}
	provider, modelOverride, ws := "openrouter", model, t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: ws, Provider: &provider, Model: &modelOverride})
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 || !strings.Contains(bodies[0], `"reasoning_effort":"max"`) {
		t.Fatalf("bodies %v", bodies)
	}
}

// A provider resolved without a key must not send a bogus credential:
// the gateway answers a placeholder Bearer token with 401 while the
// public listing reads fine without any Authorization header.
func TestKeylessCatalogReadSendsNoAuthorization(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"m1"}]}`)
	}))
	defer srv.Close()
	p := inference.NewOpenAIProvider("plain", "m1", srv.URL, "public-listing")
	public, ok := inference.WithPublicListing(p).(*inference.OpenAIProvider)
	if !ok {
		t.Fatal("WithPublicListing must keep the OpenAI provider")
	}
	if _, _, err := public.ListCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ListCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth == "" {
		t.Fatal("a keyed read still sends the key")
	}
	gotAuth = ""
	if _, _, err := public.ListCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("public listing sent %q", gotAuth)
	}
}

// A configured key still travels, so a private or regional listing keeps
// working.
func TestConfiguredKeyStillReadsTheListing(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"m1"}]}`)
	}))
	defer srv.Close()
	p := inference.NewOpenAIProvider("plain", "m1", srv.URL, "sk-real")
	if _, _, err := p.ListCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-real" {
		t.Fatalf("got %q", gotAuth)
	}
}

// A listing that publishes no metadata leaves what was learned before
// standing, so refreshing a provider that never describes its models
// cannot erase an earlier answer.
func TestRefreshKeepsMetadataWhenTheListingIsSilent(t *testing.T) {
	rt, _ := testRuntime(t)
	rt.SetCatalogFetcher(catalogFetcher(CatalogFetch{
		Models: []string{"deepseek-v4.1-flash"},
		Meta:   map[string]config.ModelMeta{"deepseek-v4.1-flash": deepseekMeta()},
	}))
	if _, err := rt.RefreshProvider(config.DefaultProviderName); err != nil {
		t.Fatal(err)
	}
	// The same provider now answers with ids only, and drops one model.
	rt.SetCatalogFetcher(catalogFetcher(CatalogFetch{Models: []string{"deepseek-v4.1-flash", "glm-5.3"}}))
	if _, err := rt.RefreshProvider(config.DefaultProviderName); err != nil {
		t.Fatal(err)
	}
	if got := rt.store.ModelMeta(config.DefaultProviderName, "deepseek-v4.1-flash"); got.PriceInput != 0.15 {
		t.Fatalf("metadata forgotten: %+v", got)
	}
	// A model the listing no longer serves loses its stale entry.
	if got := rt.store.ModelMeta(config.DefaultProviderName, "glm-5.3"); !got.Empty() {
		t.Fatalf("unlisted model kept metadata: %+v", got)
	}
}

// The published window drives the context reading the UI shows, and the
// static table still answers for a model the catalog never described.
func TestCatalogWindowDrivesSessionUsage(t *testing.T) {
	rt, _ := testRuntime(t)
	_ = rt.store.Keys.Set("openrouter", "sk-test")
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}],\"usage\":{\"prompt_tokens\":900000,\"completion_tokens\":10,\"total_tokens\":900010}}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	pointMockAt(rt, srv.URL)

	const model = "brand-new-model"
	// The static table has no entry for this model, so the window is
	// unknown until the provider's catalog publishes one.
	if _, known := inference.ContextWindow(model); known {
		t.Fatal("fixture assumption: the table must not know this model")
	}
	rt.SetCatalogFetcher(catalogFetcher(CatalogFetch{
		Models: []string{model},
		Meta:   map[string]config.ModelMeta{model: {ContextWindow: 1000000}},
	}))
	if _, err := rt.RefreshProvider("openrouter"); err != nil {
		t.Fatal(err)
	}

	provider, modelOverride, ws := "openrouter", model, t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "hi", Cwd: ws, Provider: &provider, Model: &modelOverride})
	usage, err := rt.SessionUsage(sess.SessionID, ws)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.ContextKnown || usage.ContextWindow != 1000000 {
		t.Fatalf("usage window %+v", usage)
	}
	if usage.FillPercent == nil || *usage.FillPercent < 89 || *usage.FillPercent > 91 {
		t.Fatalf("fill %v", usage.FillPercent)
	}
	// A later turn that keeps the session's model still finds the catalog
	// window: only the model override is absent, not the model.
	chatToEnd(t, rt, agent.Request{SessionID: sess.SessionID, Message: "again", Cwd: ws})
	usage, err = rt.SessionUsage(sess.SessionID, ws)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.ContextKnown || usage.ContextWindow != 1000000 {
		t.Fatalf("window lost without a per-turn model: %+v", usage)
	}
	// No level was chosen, so nothing thinking-related goes on the wire.
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 || strings.Contains(bodies[0], "reasoning_effort") {
		t.Fatalf("bodies %v", bodies)
	}
}

// Without published metadata the table answers, so a refresh that
// describes nothing cannot blank the context reading.
func TestSessionUsageFallsBackToTheTable(t *testing.T) {
	rt, _ := testRuntime(t)
	usage := rt.windowResolver(transcript.Session{})
	if w, known := usage("glm-5.3"); !known || w != 1000000 {
		t.Fatalf("table window %d known=%v", w, known)
	}
	if w, known := usage("omen-alpha"); known || w != inference.DefaultContextWindow {
		t.Fatalf("unknown window %d known=%v", w, known)
	}
	if window := rt.catalogWindow(nil, nil); window != 0 {
		t.Fatalf("catalogWindow without a provider: %d", window)
	}
}

// A published level reaches the provider the runtime builds, including
// its wire protocol: qwen3.8-max is served over Messages, which carries
// no effort, so the chip hides even though the listing names levels.
func TestCatalogProtocolHidesLevelsTheDialectCannotCarry(t *testing.T) {
	rt, _ := testRuntime(t)
	rt.SetCatalogFetcher(catalogFetcher(CatalogFetch{
		Models: []string{"qwen3.8-max", "glm-5.3"},
		Meta: map[string]config.ModelMeta{
			"qwen3.8-max": {Protocol: "anthropic", ThinkingLevels: []string{"low", "medium", "xhigh"}, ThinkingKnown: true},
			"glm-5.3":     {Protocol: "chat", ThinkingLevels: []string{"low", "high", "max"}, ThinkingKnown: true},
		},
	}))
	if _, err := rt.RefreshProvider(config.DefaultProviderName); err != nil {
		t.Fatal(err)
	}
	if v, _ := rt.ThinkingGet(config.DefaultProviderName, "qwen3.8-max"); len(v.Levels) != 0 {
		t.Fatalf("anthropic-routed levels %v", v.Levels)
	}
	if v, _ := rt.ThinkingGet(config.DefaultProviderName, "glm-5.3"); strings.Join(v.Levels, ",") != "low,high,max" {
		t.Fatalf("chat-routed levels %v", v.Levels)
	}
}

// The whole path is exercised once: a listing served over HTTP is parsed
// into metadata, stored by the refresh, and read back through
// listProviders.
func TestNetworkFetchParsesMetadata(t *testing.T) {
	rt, _ := testRuntime(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[
		 {"id":"glm-5.3","capabilities":{"reasoning":{"supported":true,"effort":["low","high","max"]}},
		  "limit":{"context":1000000,"output":131072},
		  "cost":{"text":{"input":{"price":1.4,"cache":{"read":0.26}},"output":{"price":4.4}}},
		  "api":{"chat-completions":{"base_url":"http://x"}}}]}`)
	}))
	defer srv.Close()
	// The fetcher is the only network seam the refresh has. It serves the
	// stub's body through the OpenCode catalog parser, which is the code
	// the real refresh runs against the gateway.
	rt.SetCatalogFetcher(func(ctx context.Context, _ config.Provider, _ string) (CatalogFetch, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			return CatalogFetch{}, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return CatalogFetch{}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return CatalogFetch{}, err
		}
		models, meta, err := inference.ParseCatalog(body)
		if err != nil {
			return CatalogFetch{}, err
		}
		out := make(map[string]config.ModelMeta, len(meta))
		for id, m := range meta {
			out[id] = m
		}
		return CatalogFetch{Models: models, Meta: out}, nil
	})
	if _, err := rt.RefreshProvider(config.DefaultProviderName); err != nil {
		t.Fatal(err)
	}
	got := rt.store.ModelMeta(config.DefaultProviderName, "glm-5.3")
	want := config.ModelMeta{
		ContextWindow: 1000000, MaxOutput: 131072, Protocol: "chat",
		ThinkingLevels: []string{"low", "high", "max"}, ThinkingKnown: true,
		PriceInput: 1.4, PriceOutput: 4.4, PriceCacheRead: 0.26,
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("got %s want %s", gotJSON, wantJSON)
	}
	// The stored catalog is what the picker reads.
	for _, p := range rt.ListProviders() {
		if p.Name != config.DefaultProviderName {
			continue
		}
		if p.Meta["glm-5.3"].PriceOutput != 4.4 {
			t.Fatalf("wire meta %+v", p.Meta)
		}
		if len(p.Models) != 1 || p.Models[0] != "glm-5.3" {
			t.Fatalf("models %v", p.Models)
		}
	}
}
