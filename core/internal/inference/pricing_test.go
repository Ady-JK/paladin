package inference

import (
	"math"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func TestParseCopilotPricingTiers(t *testing.T) {
	doc := `# Pricing tables
All prices are **per 1 million tokens**.
| Model | Tier | Threshold (input tokens) | Input | Cached input | Cache write | Output |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| GPT-5.6 Luna | Default | ≤ 200K | $0.20 | $0.02 | $0.25 | $1.20 |
| GPT-5.6 Luna | Long context | > 200K | $0.40 | $0.04 | $0.50 | $1.80 |
| Claude Haiku 4.5 | Default | Not applicable | $1.00 | $0.10 | $1.25 | $5.00 |
| Model | Tier | Threshold (input tokens) | Input | Cached input | Output |
| --- | --- | --- | ---: | ---: | ---: |
| Gemini 3.8 Flash[^1] | Default | Not applicable | $0.75 | $0.075 | $3.75 |
`
	prices, err := ParseCopilotPricing([]byte(doc), time.Unix(123, 0))
	if err != nil {
		t.Fatal(err)
	}
	luna := prices[priceModelKey("gpt-5.6-luna")]
	if luna == nil || len(luna.Tiers) != 2 || luna.Tiers[1].AboveInputTokens != 200000 {
		t.Fatalf("tiers: %+v", luna)
	}
	if *luna.Tiers[0].Input != 0.2 || *luna.Tiers[1].Output != 1.8 || luna.FetchedAt != 123 {
		t.Fatalf("rates: %+v", luna)
	}
	gemini := prices[priceModelKey("gemini-3.8-flash")]
	if gemini == nil || gemini.Tiers[0].CacheWrite != nil || *gemini.Tiers[0].CacheRead != 0.075 {
		t.Fatalf("gemini: %+v", gemini)
	}
}

func TestParseCopilotPricingRejectsUnrecognizedPage(t *testing.T) {
	if _, err := ParseCopilotPricing([]byte("service unavailable"), time.Now()); err == nil {
		t.Fatal("accepted an invalid table")
	}
}

func TestParseLiteLLMPricing(t *testing.T) {
	doc := []byte(`{"data":[{"model_name":"Luna","model_info":{"input_cost_per_token":0.0000002,"output_cost_per_token":0.0000012,"cache_read_input_token_cost":0.00000002,"cache_creation_input_token_cost":0.00000025,"input_cost_per_token_above_200k_tokens":0.0000004,"output_cost_per_token_above_200k_tokens":0.0000018,"cache_read_input_token_cost_above_200k_tokens":0.00000004,"cache_creation_input_token_cost_above_200k_tokens":0.0000005}},{"model_name":"No pricing","model_info":{}}]}`)
	prices, err := ParseLiteLLMPricing(doc, time.Unix(123, 0))
	if err != nil {
		t.Fatal(err)
	}
	price := prices["Luna"]
	if price == nil || len(price.Tiers) != 2 || math.Abs(*price.Tiers[0].Input-0.2) > 1e-9 || math.Abs(*price.Tiers[1].Output-1.8) > 1e-9 || price.Tiers[1].AboveInputTokens != 200000 {
		t.Fatalf("price = %+v", price)
	}
	if prices["No pricing"] != nil {
		t.Fatal("missing rates must not be shown as free")
	}
}

func TestCopilotUsageEstimate(t *testing.T) {
	input, output, read, write := 1.0, 5.0, 0.1, 1.25
	p := &OpenAIProvider{isCopilot: true, meta: config.ModelMeta{Pricing: &config.ModelPricing{FetchedAt: time.Now().Unix(), Tiers: []config.PriceTier{{Input: &input, Output: &output, CacheRead: &read, CacheWrite: &write}}}}}
	usage := &Usage{PromptTokens: 1000, CompletionTokens: 100, CacheReadTokens: 200, CacheWriteTokens: 100, CacheReadKnown: true, CacheWriteKnown: true}
	p.estimateCopilotUsage(usage, false)
	if !usage.HasCost || !usage.CostEstimated || math.Abs(usage.ListCost-0.001345) > 1e-12 {
		t.Fatalf("estimate = %+v", usage)
	}
	// Copilot reports cache reads but never cache writes: still priced.
	noWrite := &Usage{PromptTokens: 1000, CompletionTokens: 100, CacheReadTokens: 200, CacheReadKnown: true}
	p.estimateCopilotUsage(noWrite, false)
	if !noWrite.HasCost || !noWrite.CostEstimated || math.Abs(noWrite.ListCost-0.00132) > 1e-12 {
		t.Fatalf("estimate without cache writes = %+v", noWrite)
	}
	missing := &Usage{PromptTokens: 1000, CompletionTokens: 100}
	p.estimateCopilotUsage(missing, false)
	if missing.HasCost {
		t.Fatal("missing cache-read information must not imply zero usage")
	}
}

func TestLiteLLMContextMetadata(t *testing.T) {
	doc := []byte(`{"data":[{"model_name":"sample","model_info":{"max_input_tokens":128000,"max_output_tokens":16000}},{"model_name":"ambiguous","model_info":{"max_input_tokens":200000}},{"model_name":"ambiguous","model_info":{"max_input_tokens":128000}}]}`)
	meta, err := ParseLiteLLMContext(doc)
	if err != nil || meta["sample"].ContextWindow != 128000 || meta["sample"].MaxOutput != 16000 {
		t.Fatalf("context: %+v %v", meta, err)
	}
	if meta["ambiguous"].ContextWindow != 0 {
		t.Fatal("ambiguous context must not be guessed")
	}
}
