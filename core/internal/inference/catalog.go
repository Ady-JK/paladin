// catalog.go reads what a provider's own model listing says about each
// model: the wire dialect that serves it, the context and output limits,
// the thinking levels it accepts and its prices. The metadata travels
// with the catalog rather than in a hand-written table, so a provider
// that adds or reprices a model is picked up by the next refresh.
//
// OpenCode Go and GitHub Copilot publish model capabilities. Other
// providers get ids only, and readers fall back to built-in tables.

package inference

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// CatalogMeta is one model's metadata as the provider documented it.
// Zero fields mean the listing did not say; see config.ModelMeta.
type CatalogMeta = config.ModelMeta

// catalogEntry is one model of a listing. OpenCode's shape is a superset
// of the OpenAI /v1/models object, so the plain fields work for both.
type catalogEntry struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Capabili struct {
		Reasoning struct {
			Supported *bool    `json:"supported"`
			Effort    []string `json:"effort"`
			Toggle    *bool    `json:"toggle"`
		} `json:"reasoning"`
	} `json:"capabilities"`
	Limit struct {
		Context int64 `json:"context"`
		Output  int64 `json:"output"`
	} `json:"limit"`
	Cost catalogCost `json:"cost"`
	API  map[string]struct {
		BaseURL string `json:"base_url"`
	} `json:"api"`
}

// catalogCost is the OpenCode price envelope: USD per million tokens,
// with an optional cache sub-object on the input side.
type catalogCost struct {
	Text *struct {
		Input *struct {
			Price float64 `json:"price"`
			Cache *struct {
				Read  catalogPrice `json:"read"`
				Write catalogPrice `json:"write"`
			} `json:"cache"`
		} `json:"input"`
		Output *struct {
			Price float64 `json:"price"`
		} `json:"output"`
	} `json:"text"`
}

// catalogPrice is a price the listing may publish two ways: a plain
// number, or a map keyed by how long the price buys ("5m", "30m", "12h",
// ...). The map collapses to the shortest-lived bucket, the one a
// request pays unless it explicitly asks for a longer write; the longer
// buckets cost more, never less. A key the code cannot read as a
// duration falls back to the cheapest bucket, so an unknown scheme
// still yields a conservative price instead of failing the refresh.
type catalogPrice struct {
	value float64
}

func (p *catalogPrice) UnmarshalJSON(body []byte) error {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, "{") {
		var buckets map[string]float64
		if err := json.Unmarshal(body, &buckets); err != nil {
			return err
		}
		p.value = shortestBucket(buckets)
		return nil
	}
	return json.Unmarshal(body, &p.value)
}

func (p catalogPrice) float() float64 {
	return p.value
}

// shortestBucket picks the price of the shortest-lived bucket.
func shortestBucket(buckets map[string]float64) float64 {
	best := 0.0
	var bestAt time.Duration
	found := false
	for key, price := range buckets {
		life, err := time.ParseDuration(key)
		if err != nil {
			continue
		}
		if !found || life < bestAt {
			found, best, bestAt = true, price, life
		}
	}
	if found {
		return best
	}
	// No readable duration: take the cheapest bucket rather than one at
	// random, so the estimate never reads high just because of the key
	// scheme.
	for _, price := range buckets {
		if !found || price < best {
			found, best = true, price
		}
	}
	return best
}

// The price accessors read the envelope defensively: a listing may omit
// any level, and a missing price is zero (free) rather than an error.
func (c catalogCost) input() float64 {
	if c.Text == nil || c.Text.Input == nil {
		return 0
	}
	return c.Text.Input.Price
}

func (c catalogCost) output() float64 {
	if c.Text == nil || c.Text.Output == nil {
		return 0
	}
	return c.Text.Output.Price
}

func (c catalogCost) cacheRead() float64 {
	if c.Text == nil || c.Text.Input == nil || c.Text.Input.Cache == nil {
		return 0
	}
	return c.Text.Input.Cache.Read.float()
}

func (c catalogCost) cacheWrite() float64 {
	if c.Text == nil || c.Text.Input == nil || c.Text.Input.Cache == nil {
		return 0
	}
	return c.Text.Input.Cache.Write.float()
}

// catalogProtocol maps the key an entry documents its endpoint under to
// the protocol name the provider routes with.
func catalogProtocol(apiKeys []string) string {
	switch {
	case len(apiKeys) == 0:
		return ""
	case containsKey(apiKeys, "responses"):
		return protocolResponses
	case containsKey(apiKeys, "messages"):
		return protocolAnthropic
	case containsKey(apiKeys, "generate-content"):
		return protocolGoogle
	case containsKey(apiKeys, "chat-completions"):
		return protocolChat
	default:
		return ""
	}
}

func containsKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// effortOrder ranks the levels so a listing that names them out of order
// still reads weakest first, which is what the UI slider assumes.
var effortOrder = []string{
	EffortNone, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax,
}

// normalizeEfforts reads the listing's reasoning block into the levels
// the UI offers, weakest first, dropping anything it does not know. The
// second return marks the block as authoritative: the listing described
// this model's reasoning, so an empty list really means "none to pick".
// A model that reasons but only exposes an on/off switch gets ["none"],
// which is the one level a slider can honour.
func normalizeEfforts(entry catalogEntry) ([]string, bool) {
	reasons := entry.Capabili.Reasoning
	supported := reasons.Supported != nil && *reasons.Supported
	toggle := reasons.Toggle != nil && *reasons.Toggle
	if reasons.Supported == nil && len(reasons.Effort) == 0 && reasons.Toggle == nil {
		// The listing said nothing about reasoning for this model.
		return nil, false
	}
	if !supported {
		return []string{}, true
	}
	levels := make([]string, 0, len(reasons.Effort))
	for _, want := range effortOrder {
		for _, got := range reasons.Effort {
			if strings.EqualFold(strings.TrimSpace(got), want) {
				levels = append(levels, want)
				break
			}
		}
	}
	if len(levels) > 0 {
		return levels, true
	}
	if toggle {
		return []string{EffortNone}, true
	}
	return []string{}, true
}

// meta builds the metadata stored with the catalog. apiKeys are the
// endpoints the entry documents; the caller collects them because a
// map's keys range in no defined order.
func (e catalogEntry) meta(apiKeys []string) CatalogMeta {
	levels, known := normalizeEfforts(e)
	return CatalogMeta{
		ContextWindow:   e.Limit.Context,
		MaxOutput:       e.Limit.Output,
		Protocol:        catalogProtocol(apiKeys),
		ThinkingLevels:  levels,
		ThinkingKnown:   known,
		PriceInput:      e.Cost.input(),
		PriceOutput:     e.Cost.output(),
		PriceCacheRead:  e.Cost.cacheRead(),
		PriceCacheWrite: e.Cost.cacheWrite(),
	}
}

// ParseCatalog reads a model listing into ids plus their metadata. It
// accepts the OpenAI {data:[…]} envelope and a bare array, and never
// fails on unknown fields: a listing that carries no metadata yields ids
// with empty metadata, which reads as "the listing said nothing".
func ParseCatalog(body []byte) ([]string, map[string]CatalogMeta, error) {
	var wire struct {
		Data []catalogEntry `json:"data"`
	}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var entries []catalogEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return nil, nil, fmt.Errorf("decode catalog: %w", err)
		}
		wire.Data = entries
	} else if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, fmt.Errorf("decode catalog: %w", err)
	}
	var ids []string
	meta := make(map[string]CatalogMeta, len(wire.Data))
	for _, entry := range wire.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		ids = append(ids, id)
		apiKeys := make([]string, 0, len(entry.API))
		for key := range entry.API {
			apiKeys = append(apiKeys, key)
		}
		if m := entry.meta(apiKeys); !m.Empty() {
			meta[id] = m
		}
	}
	if len(meta) == 0 {
		meta = nil
	}
	return ids, meta, nil
}
