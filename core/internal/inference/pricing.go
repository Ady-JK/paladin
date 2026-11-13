package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Ady-JK/paladin/core/internal/config"
)

const CopilotPricingURL = "https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing.md"

func priceModelKey(name string) string {
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i]
	}
	var b strings.Builder
	for _, c := range strings.ToLower(name) {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func dollarRate(raw string) *float64 {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, "$") {
		return nil
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimPrefix(value, "$"), ",", ""), 64)
	if err != nil || n < 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return nil
	}
	return &n
}

func thresholdAbove(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if !strings.HasPrefix(value, ">") {
		return 0, true
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, ">"))
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(strings.ToUpper(value), "K"):
		multiplier = 1000
		value = value[:len(value)-1]
	case strings.HasSuffix(strings.ToUpper(value), "M"):
		multiplier = 1000000
		value = value[:len(value)-1]
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(value), ",", ""), 10, 64)
	if err != nil || n < 0 || n > math.MaxInt64/multiplier {
		return 0, false
	}
	return n * multiplier, true
}

func pricingCells(line string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// ParseCopilotPricing reads GitHub's rendered Markdown table; an unknown
// layout fails closed rather than inventing rates for account usage.
func ParseCopilotPricing(doc []byte, at time.Time) (map[string]*config.ModelPricing, error) {
	prices := make(map[string]*config.ModelPricing)
	var columns map[string]int
	for _, line := range strings.Split(string(doc), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := pricingCells(line)
		if len(cells) == 0 {
			continue
		}
		if strings.EqualFold(cells[0], "model") {
			columns = make(map[string]int)
			for i, c := range cells {
				columns[strings.ToLower(c)] = i
			}
			continue
		}
		if columns == nil {
			continue
		}
		input, inputOK := columns["input"]
		output, outputOK := columns["output"]
		if !inputOK || !outputOK || input >= len(cells) || output >= len(cells) {
			continue
		}
		in, out := dollarRate(cells[input]), dollarRate(cells[output])
		if in == nil || out == nil {
			continue
		}
		key := priceModelKey(cells[0])
		if key == "" {
			continue
		}
		tier := config.PriceTier{Input: in, Output: out}
		if i, ok := columns["cached input"]; ok && i < len(cells) {
			tier.CacheRead = dollarRate(cells[i])
		}
		if i, ok := columns["cache write"]; ok && i < len(cells) {
			tier.CacheWrite = dollarRate(cells[i])
		}
		if i, ok := columns["threshold (input tokens)"]; ok && i < len(cells) {
			threshold, valid := thresholdAbove(cells[i])
			if !valid {
				continue
			}
			tier.AboveInputTokens = threshold
		}
		entry := prices[key]
		if entry == nil {
			entry = &config.ModelPricing{Source: CopilotPricingURL, FetchedAt: at.Unix()}
			prices[key] = entry
		}
		entry.Tiers = append(entry.Tiers, tier)
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("GitHub's pricing table was not recognized")
	}
	for _, p := range prices {
		sort.Slice(p.Tiers, func(i, j int) bool { return p.Tiers[i].AboveInputTokens < p.Tiers[j].AboveInputTokens })
	}
	return prices, nil
}

func perMillion(raw json.RawMessage) *float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	value *= 1_000_000
	if math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func litePriceTier(fields map[string]json.RawMessage, suffix string, threshold int64) config.PriceTier {
	return config.PriceTier{
		AboveInputTokens: threshold,
		Input:            perMillion(fields["input_cost_per_token"+suffix]),
		Output:           perMillion(fields["output_cost_per_token"+suffix]),
		CacheRead:        perMillion(fields["cache_read_input_token_cost"+suffix]),
		CacheWrite:       perMillion(fields["cache_creation_input_token_cost"+suffix]),
	}
}

// ParseLiteLLMPricing preserves missing rates and refuses ambiguous aliases.
func ParseLiteLLMPricing(doc []byte, at time.Time) (map[string]*config.ModelPricing, error) {
	var wire struct {
		Data []struct {
			Name string                     `json:"model_name"`
			Info map[string]json.RawMessage `json:"model_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(doc, &wire); err != nil || wire.Data == nil {
		return nil, fmt.Errorf("invalid LiteLLM pricing catalog")
	}
	prices := make(map[string]*config.ModelPricing)
	ambiguous := make(map[string]bool)
	for _, item := range wire.Data {
		if item.Name == "" || ambiguous[item.Name] {
			continue
		}
		base := litePriceTier(item.Info, "", 0)
		if base.Input == nil && base.Output == nil && base.CacheRead == nil && base.CacheWrite == nil {
			continue
		}
		price := &config.ModelPricing{Source: "litellm", FetchedAt: at.Unix(), Tiers: []config.PriceTier{base}}
		thresholds := map[int64]bool{}
		for key := range item.Info {
			const prefix = "input_cost_per_token_above_"
			const suffix = "k_tokens"
			if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
				continue
			}
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix), 10, 64)
			if err == nil && n > 0 && n <= math.MaxInt64/1000 {
				thresholds[n*1000] = true
			}
		}
		for threshold := range thresholds {
			suffix := fmt.Sprintf("_above_%dk_tokens", threshold/1000)
			tier := litePriceTier(item.Info, suffix, threshold)
			if tier.Input != nil || tier.Output != nil || tier.CacheRead != nil || tier.CacheWrite != nil {
				price.Tiers = append(price.Tiers, tier)
			}
		}
		sort.Slice(price.Tiers, func(i, j int) bool { return price.Tiers[i].AboveInputTokens < price.Tiers[j].AboveInputTokens })
		if previous := prices[item.Name]; previous != nil && !reflect.DeepEqual(previous.Tiers, price.Tiers) {
			delete(prices, item.Name)
			ambiguous[item.Name] = true
			continue
		}
		prices[item.Name] = price
	}
	return prices, nil
}

// FetchCopilotPricing reads public GitHub reference prices without credentials.
func FetchCopilotPricing(ctx context.Context) (map[string]*config.ModelPricing, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CopilotPricingURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub pricing unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub pricing status %d", resp.StatusCode)
	}
	body, err := readBody(resp.Body, 2<<20)
	if err != nil {
		return nil, err
	}
	return ParseCopilotPricing(body, time.Now().UTC())
}

func CopilotPriceFor(prices map[string]*config.ModelPricing, model string) *config.ModelPricing {
	return prices[priceModelKey(model)]
}

// ParseLiteLLMContext extracts published input and output limits. Conflicting
// deployments sharing an alias are left unknown rather than guessed.
func ParseLiteLLMContext(doc []byte) (map[string]config.ModelMeta, error) {
	var wire struct {
		Data []struct {
			Name string `json:"model_name"`
			Info struct {
				Input  int64 `json:"max_input_tokens"`
				Output int64 `json:"max_output_tokens"`
			} `json:"model_info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(doc, &wire); err != nil || wire.Data == nil {
		return nil, fmt.Errorf("invalid LiteLLM model catalog")
	}
	result := make(map[string]config.ModelMeta)
	ambiguous := make(map[string]bool)
	for _, item := range wire.Data {
		if item.Name == "" || ambiguous[item.Name] {
			continue
		}
		value := config.ModelMeta{ContextWindow: item.Info.Input, MaxOutput: item.Info.Output}
		if value.ContextWindow < 0 || value.MaxOutput < 0 {
			continue
		}
		if previous, ok := result[item.Name]; ok && (previous.ContextWindow != value.ContextWindow || previous.MaxOutput != value.MaxOutput) {
			delete(result, item.Name)
			ambiguous[item.Name] = true
			continue
		}
		result[item.Name] = value
	}
	return result, nil
}

// FetchLiteLLMPricing asks only the configured proxy, on the same origin.
func FetchLiteLLMPricing(ctx context.Context, baseURL, key string) (map[string]config.ModelMeta, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid LiteLLM URL")
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1") + "/model/info"
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if !config.IsPlaceholderKey(key) {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LiteLLM pricing unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LiteLLM pricing status %d", resp.StatusCode)
	}
	body, err := readBody(resp.Body, 4<<20)
	if err != nil {
		return nil, err
	}
	prices, err := ParseLiteLLMPricing(body, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	meta, err := ParseLiteLLMContext(body)
	if err != nil {
		return nil, err
	}
	for id, pricing := range prices {
		value := meta[id]
		value.Pricing = pricing
		meta[id] = value
	}
	return meta, nil
}

// estimateCopilotUsage uses published list prices, not an account invoice.
// Missing cache-read counts or rates make the estimate unavailable. Copilot's
// chat endpoint never reports cache writes, so an unreported write count is
// taken as zero (the estimate is then a lower bound for cache-write surcharges).
func (p *OpenAIProvider) estimateCopilotUsage(u *Usage, separateInput bool) *Usage {
	if !p.isCopilot || u == nil || u.HasCost || !u.CacheReadKnown {
		return u
	}
	pricing := p.meta.Pricing
	if pricing == nil || pricing.FetchedAt <= 0 || time.Since(time.Unix(pricing.FetchedAt, 0)) > 30*24*time.Hour {
		return u
	}
	totalInput := u.PromptTokens
	if separateInput {
		totalInput += u.CacheReadTokens + u.CacheWriteTokens
	}
	uncached := totalInput - u.CacheReadTokens - u.CacheWriteTokens
	if uncached < 0 {
		return u
	}
	var tier *config.PriceTier
	for i := range pricing.Tiers {
		t := &pricing.Tiers[i]
		if totalInput > t.AboveInputTokens || (t.AboveInputTokens == 0 && totalInput == 0) {
			tier = t
		}
	}
	if tier == nil || tier.Input == nil || tier.Output == nil || (u.CacheReadTokens > 0 && tier.CacheRead == nil) || (u.CacheWriteTokens > 0 && tier.CacheWrite == nil) {
		return u
	}
	cost := float64(uncached)**tier.Input + float64(u.CompletionTokens)**tier.Output
	if u.CacheReadTokens > 0 {
		cost += float64(u.CacheReadTokens) * *tier.CacheRead
	}
	if u.CacheWriteTokens > 0 {
		cost += float64(u.CacheWriteTokens) * *tier.CacheWrite
	}
	u.ListCost, u.HasCost, u.CostEstimated = cost/1e6, true, true
	return u
}
