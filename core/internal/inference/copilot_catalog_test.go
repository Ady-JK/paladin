package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func TestCopilotCatalogRoutesSupportedModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
          {"id":"claude-haiku","model_picker_enabled":true,"supported_endpoints":["/v1/messages","/chat/completions"],"capabilities":{"type":"chat","limits":{"max_context_window_tokens":200000,"max_output_tokens":64000}}},
			{"id":"gpt-5.6-luna","model_picker_enabled":true,"supported_endpoints":["/responses","ws:/responses"],"capabilities":{"type":"chat","limits":{"max_context_window_tokens":328000,"max_output_tokens":128000},"supports":{"reasoning_effort":["low","high"]}}},
          {"id":"unknown-chat","model_picker_enabled":true,"supported_endpoints":["/chat/completions"],"capabilities":{"type":"chat"}},
          {"id":"future-model","model_picker_enabled":true,"supported_endpoints":["/responses"],"capabilities":{"type":"chat"}},
          {"id":"gpt-9","model_picker_enabled":true,"supported_endpoints":["/chat/completions"],"capabilities":{"type":"chat"}},
          {"id":"messages-only","model_picker_enabled":true,"supported_endpoints":["/v1/messages"],"capabilities":{"type":"chat"}},
          {"id":"hidden","model_picker_enabled":false,"supported_endpoints":["/chat/completions"],"capabilities":{"type":"chat"}},
          {"id":"disabled","model_picker_enabled":true,"policy":{"state":"disabled"},"supported_endpoints":["/chat/completions"],"capabilities":{"type":"chat"}},
          {"id":"decisions","model_picker_enabled":true,"supported_endpoints":["/v1/decisions"],"capabilities":{"type":"decisions"}},
          {"id":"embed","capabilities":{"type":"embeddings"}}
        ]}`))
	}))
	defer server.Close()
	base, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "", server.URL, "access-token")
	if err != nil {
		t.Fatal(err)
	}
	models, meta, err := base.(*OpenAIProvider).ListCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 6 {
		raw, _ := json.Marshal(models)
		t.Fatalf("models %s", raw)
	}
	want := map[string]string{"claude-haiku": protocolChat, "gpt-5.6-luna": protocolResponses, "unknown-chat": protocolChat, "future-model": protocolResponses, "gpt-9": protocolChat, "messages-only": protocolAnthropic}
	for _, id := range models {
		m, ok := meta[id]
		if !ok || m.Protocol != want[id] {
			t.Errorf("%s: metadata %+v", id, m)
			continue
		}
		p, err := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", id, server.URL, "access-token", m)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.(*OpenAIProvider).protocol; got != want[id] {
			t.Errorf("%s: route %s, want %s", id, got, want[id])
		}
	}
	if meta["gpt-5.6-luna"].ContextWindow != 328000 || meta["gpt-5.6-luna"].MaxOutput != 128000 {
		t.Fatalf("wrong limits: %+v", meta["gpt-5.6-luna"])
	}
	levels := ModelEffortLevels(config.ApiTypeCopilot, server.URL, "gpt-5.6-luna", meta["gpt-5.6-luna"])
	if len(levels) != 2 || levels[0] != "low" || levels[1] != "high" {
		t.Fatalf("thinking levels %v", levels)
	}
}

func TestCopilotOldCatalogRoutesResponsesModels(t *testing.T) {
	p, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "gpt-5.6-luna", "https://api.githubcopilot.com", "access-token")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.(*OpenAIProvider).protocol; got != protocolResponses {
		t.Fatalf("old catalog route %q", got)
	}
}

func TestCopilotMessagesOnlyModel(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != "Bearer access-token" || r.Header.Get("Openai-Intent") != "messages-proxy" || r.Header.Get("Copilot-Integration-Id") == "" {
			t.Errorf("invalid messages request")
		}
		var body struct {
			Stream    bool `json:"stream"`
			MaxTokens int  `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.MaxTokens != 4096 {
			t.Errorf("max tokens = %d", body.MaxTokens)
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}]}`))
	}))
	defer server.Close()
	p, err := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "messages-only", server.URL, "access-token", config.ModelMeta{Protocol: protocolAnthropic, MaxOutput: 4096})
	if err != nil {
		t.Fatal(err)
	}
	messages := []LlmMessage{{Role: "user", Content: "hello"}}
	result, err := p.CompleteTools(context.Background(), "", messages, nil, "test")
	if err != nil || result.Content != "hello" {
		t.Fatalf("completion: %+v, %v", result, err)
	}
	chunks := make(chan Chunk, 16)
	done := make(chan struct{})
	err = p.StreamTools(context.Background(), "", messages, nil, "test", chunks, done)
	close(chunks)
	close(done)
	if err != nil {
		t.Fatal(err)
	}
	var output string
	for chunk := range chunks {
		output += chunk.Text
	}
	if output != "hello" || requests != 2 {
		t.Fatalf("output %q requests %d", output, requests)
	}
}

func TestCopilotMessagesIncludesUserImage(t *testing.T) {
	image := imageMessages(t)[1].Image
	provider := &OpenAIProvider{isCopilot: true}
	req := provider.anthropicRequest("", []LlmMessage{{Role: "user", Content: "describe", Image: image}}, nil)
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 || req.Messages[0].Content[1].Source == nil {
		t.Fatal("Copilot Messages request did not include the user's image")
	}
}

func TestCopilotFutureModelFollowsAdvertisedEndpoint(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"future-example","model_picker_enabled":true,"supported_endpoints":["/responses"],"capabilities":{"type":"chat"}}]}`))
		case "/responses":
			called = true
			_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}`))
		default:
			t.Errorf("unexpected endpoint %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	p, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "", server.URL, "access-token")
	if err != nil {
		t.Fatal(err)
	}
	_, meta, err := p.(*OpenAIProvider).ListCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, err = BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", "future-example", server.URL, "access-token", meta["future-example"])
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.CompleteTools(context.Background(), "", []LlmMessage{{Role: "user", Content: "hello"}}, nil, "test")
	if err != nil || !called || result.Content != "hello" {
		t.Fatalf("response=%+v, called=%v, err=%v", result, called, err)
	}
}

func TestCopilotCatalogSeparatesPromptAndTotalContext(t *testing.T) {
	meta, include, err := parseCopilotModel("claude-sonnet-5.5", `{"capabilities":{"limits":{"max_context_window_tokens":328000,"max_prompt_tokens":200000,"max_output_tokens":128000}}}`)
	if err != nil || !include || meta.ContextWindow != 328000 || meta.MaxInput != 200000 {
		t.Fatalf("metadata: %+v %v", meta, err)
	}
	if window, known := WindowFor(meta, "claude-sonnet-5.5"); !known || window != 200000 {
		t.Fatalf("prompt window: %d, %t", window, known)
	}
}

func TestCopilotLongContextPricesAndLimits(t *testing.T) {
	const raw = `{"capabilities":{"limits":{"max_context_window_tokens":1000000,"max_prompt_tokens":936000,"max_output_tokens":128000}},"billing":{"token_prices":{"batch_size":1000000,"default":{"input_price":200,"output_price":1000,"cache_read_price":10,"cache_write_price":250,"max_prompt_tokens":200000},"long_context":{"input_price":200,"output_price":1000,"cache_read_price":10,"cache_write_price":250,"max_prompt_tokens":936000}}}}`
	meta, _, err := parseCopilotModel("claude-sonnet-5.5", raw)
	if err != nil || meta.MaxInput != 936000 || meta.DefaultInput != 200000 || meta.LongContextSurcharge || meta.Pricing == nil || len(meta.Pricing.Tiers) != 1 || *meta.Pricing.Tiers[0].Input != 2 {
		t.Fatalf("metadata: %+v %v", meta, err)
	}
}

func TestCopilotLongContextSurcharge(t *testing.T) {
	const raw = `{"capabilities":{"limits":{"max_context_window_tokens":1050000,"max_prompt_tokens":922000}},"billing":{"token_prices":{"batch_size":1000000,"default":{"input_price":20,"output_price":120,"max_prompt_tokens":200000},"long_context":{"input_price":40,"output_price":180,"max_prompt_tokens":922000}}}}`
	meta, _, err := parseCopilotModel("gpt-5.6-luna", raw)
	if err != nil || !meta.LongContextSurcharge || meta.Pricing == nil || len(meta.Pricing.Tiers) != 2 || meta.Pricing.Tiers[1].AboveInputTokens != 200000 || *meta.Pricing.Tiers[1].Input != 0.4 {
		t.Fatalf("surcharged pricing: %+v %v", meta, err)
	}
}

func TestCopilotCatalogFallsBackWhenVersionUnsupported(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-GitHub-Api-Version") != "" {
			http.Error(w, "unsupported version", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"sample","capabilities":{"limits":{"max_prompt_tokens":200000}}}]}`))
	}))
	defer server.Close()
	provider, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "", server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	models, meta, err := provider.(*OpenAIProvider).ListCatalog(context.Background())
	if err != nil || calls != 2 || len(models) != 1 || meta["sample"].MaxInput != 200000 {
		t.Fatalf("fallback calls=%d models=%v meta=%+v error=%v", calls, models, meta, err)
	}
}
