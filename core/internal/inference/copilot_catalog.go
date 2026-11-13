package inference

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// copilotModel contains only fields needed to route and present a model.
type copilotPriceTier struct {
	Input           *float64 `json:"input_price"`
	Output          *float64 `json:"output_price"`
	CacheRead       *float64 `json:"cache_read_price"`
	CacheReadLegacy *float64 `json:"cache_price"`
	CacheWrite      *float64 `json:"cache_write_price"`
	MaxInput        int64    `json:"max_prompt_tokens"`
}

type copilotModel struct {
	Billing struct {
		TokenPrices struct {
			BatchSize float64           `json:"batch_size"`
			Default   *copilotPriceTier `json:"default"`
			Long      *copilotPriceTier `json:"long_context"`
		} `json:"token_prices"`
	} `json:"billing"`
	PickerEnabled *bool    `json:"model_picker_enabled"`
	Endpoints     []string `json:"supported_endpoints"`
	Policy        struct {
		State string `json:"state"`
	} `json:"policy"`
	Capabilities struct {
		Type   string `json:"type"`
		Limits struct {
			Context int64 `json:"max_context_window_tokens"`
			Input   int64 `json:"max_prompt_tokens"`
			Output  int64 `json:"max_output_tokens"`
		} `json:"limits"`
		Supports struct {
			ReasoningEffort *[]string `json:"reasoning_effort"`
		} `json:"supports"`
	} `json:"capabilities"`
}

func copilotEndpointProtocol(endpoints []string) string {
	for _, endpoint := range endpoints {
		if endpoint == "/chat/completions" || endpoint == "/v1/chat/completions" {
			return protocolChat
		}
	}
	for _, endpoint := range endpoints {
		if endpoint == "/responses" || endpoint == "/v1/responses" {
			return protocolResponses
		}
	}
	for _, endpoint := range endpoints {
		if endpoint == "/v1/messages" {
			return protocolAnthropic
		}
	}
	return ""
}

// copilotProtocol uses the catalog first, with a compatibility fallback for
// sessions created before their first metadata-aware catalog refresh.
func copilotProtocol(model string, meta config.ModelMeta) string {
	switch meta.Protocol {
	case protocolChat, protocolResponses, protocolAnthropic:
		return meta.Protocol
	}
	version := gptVersion.FindStringSubmatch(model)
	if version != nil {
		major, minor := atoi(version[1]), atoi(version[2])
		if major > 5 || (major == 5 && minor >= 3) {
			return protocolResponses
		}
	}
	return protocolChat
}

func parseCopilotModel(id, raw string) (config.ModelMeta, bool, error) {
	var model copilotModel
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		return config.ModelMeta{}, false, err
	}
	if model.PickerEnabled != nil && !*model.PickerEnabled {
		return config.ModelMeta{}, false, nil
	}
	if model.Capabilities.Type != "" && model.Capabilities.Type != "chat" {
		return config.ModelMeta{}, false, nil
	}
	if strings.EqualFold(model.Policy.State, "disabled") || strings.EqualFold(model.Policy.State, "blocked") {
		return config.ModelMeta{}, false, nil
	}
	if strings.HasPrefix(strings.ToLower(id), "text-embedding-") {
		return config.ModelMeta{}, false, nil
	}
	protocol := copilotEndpointProtocol(model.Endpoints)
	if len(model.Endpoints) > 0 && protocol == "" {
		return config.ModelMeta{}, false, nil
	}
	if protocol == "" {
		protocol = copilotProtocol(id, config.ModelMeta{})
	}
	meta := config.ModelMeta{Protocol: protocol, ContextWindow: model.Capabilities.Limits.Context, MaxInput: model.Capabilities.Limits.Input, MaxOutput: model.Capabilities.Limits.Output}

	prices := model.Billing.TokenPrices
	if prices.Default != nil && prices.Default.MaxInput > 0 && prices.Default.MaxInput < meta.MaxInput {
		meta.DefaultInput = prices.Default.MaxInput
	}
	if prices.Default != nil && prices.Default.Input != nil && prices.Default.Output != nil {
		batch := prices.BatchSize
		if batch <= 0 {
			batch = 1000000
		}
		scale := 1000000 / batch / 100
		base := convertCopilotPrice(prices.Default, scale)
		tiers := []config.PriceTier{base}
		if prices.Long != nil && prices.Long.Input != nil && prices.Long.Output != nil && meta.DefaultInput > 0 {
			long := convertCopilotPrice(prices.Long, scale)
			meta.LongContextSurcharge = !reflect.DeepEqual(base, long)
			if meta.LongContextSurcharge {
				long.AboveInputTokens = meta.DefaultInput
				tiers = append(tiers, long)
			}
		} else if meta.DefaultInput > 0 {
			meta.LongContextSurcharge = true
		}
		meta.Pricing = &config.ModelPricing{Source: "github-copilot-api", FetchedAt: time.Now().Unix(), Tiers: tiers}
	}
	if model.Capabilities.Supports.ReasoningEffort != nil {
		meta.ThinkingKnown = true
		for _, want := range effortOrder {
			for _, level := range *model.Capabilities.Supports.ReasoningEffort {
				if strings.EqualFold(want, level) {
					meta.ThinkingLevels = append(meta.ThinkingLevels, want)
					break
				}
			}
		}
	}
	return meta, true, nil
}

func convertCopilotPrice(tier *copilotPriceTier, scale float64) config.PriceTier {
	scaleRate := func(value *float64) *float64 {
		if value == nil || *value < 0 {
			return nil
		}
		n := *value * scale
		return &n
	}
	read := tier.CacheRead
	if read == nil {
		read = tier.CacheReadLegacy
	}
	return config.PriceTier{Input: scaleRate(tier.Input), Output: scaleRate(tier.Output), CacheRead: scaleRate(read), CacheWrite: scaleRate(tier.CacheWrite)}
}
