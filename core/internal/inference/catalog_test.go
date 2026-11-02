package inference

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// opencodeListing is a slice of the real /inference/go/v1/models body:
// one chat model with a peak-hours multiplier, one Responses model, one
// Anthropic-routed model with an effort list, one toggle-only model and
// one Gemini-routed model. Prices are USD per million tokens.
const opencodeListing = `{"object":"list","data":[
 {"id":"deepseek-v4.1-flash","object":"model","capabilities":{"tools":{"supported":true},
   "reasoning":{"supported":true,"effort":["low","high","max"]},"structured_output":true},
  "limit":{"context":1000000,"output":384000},
  "cost":{"text":{"input":{"price":0.15,"cache":{"read":0.003}},"output":{"price":0.6}},
   "multipliers":[{"when":{"utc_days":{"in":["mon"]}},"factor":2}]},
  "api":{"chat-completions":{"base_url":"https://opencode.ai/inference/go/openai/v1"}}},
 {"id":"gpt-6-luna","capabilities":{"reasoning":{"supported":true,
   "effort":["none","low","medium","high","xhigh","max"]}},
  "limit":{"context":1050000,"output":128000},
  "cost":{"text":{"input":{"price":0.1,"cache":{"read":0.01,"write":0.125}},"output":{"price":0.5}}},
  "api":{"responses":{"base_url":"https://opencode.ai/inference/go/openai/v1"}}},
 {"id":"claude-haiku-5-5","capabilities":{"reasoning":{"supported":true,"toggle":true,
   "effort":["low","medium","high","xhigh","max"]}},
  "limit":{"context":1000000,"output":128000},
  "cost":{"text":{"input":{"price":0.1},"output":{"price":0.5}}},
  "api":{"messages":{"base_url":"https://opencode.ai/inference/go/anthropic/v1"}}},
 {"id":"minimax-m3","capabilities":{"reasoning":{"supported":true,"toggle":true}},
  "limit":{"context":1000000,"output":131072},
  "cost":{"text":{"input":{"price":0.3,"cache":{"read":0.06}},"output":{"price":1.2}}},
  "api":{"messages":{"base_url":"https://opencode.ai/inference/go/anthropic/v1"}}},
 {"id":"gemini-4-argon","capabilities":{"reasoning":{"supported":true,
   "effort":["low","medium","high"]}},
  "limit":{"context":1048576,"output":65536},
  "cost":{"text":{"input":{"price":2,"cache":{"read":0.1}},"output":{"price":10}}},
  "api":{"generate-content":{"base_url":"https://opencode.ai/inference/go/google/v1beta"}}}
]}`

func TestParseCatalogReadsEveryMode(t *testing.T) {
	ids, meta, err := ParseCatalog([]byte(opencodeListing))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 5 || ids[0] != "deepseek-v4.1-flash" {
		t.Fatalf("ids %v", ids)
	}
	cases := []struct {
		id       string
		protocol string
		context  int64
		output   int64
		levels   string
		known    bool
		in, out  float64
		read     float64
		write    float64
	}{
		// A chat model with an effort list and no cache write price.
		{"deepseek-v4.1-flash", protocolChat, 1000000, 384000, "low,high,max", true, 0.15, 0.6, 0.003, 0},
		// A Responses model that can also turn thinking off.
		{"gpt-6-luna", protocolResponses, 1050000, 128000, "none,low,medium,high,xhigh,max", true, 0.1, 0.5, 0.01, 0.125},
		// An Anthropic-routed model.
		{"claude-haiku-5-5", protocolAnthropic, 1000000, 128000, "low,medium,high,xhigh,max", true, 0.1, 0.5, 0, 0},
		// Toggle only: thinking can be turned off, with no depth to pick.
		{"minimax-m3", protocolAnthropic, 1000000, 131072, "none", true, 0.3, 1.2, 0.06, 0},
		// Gemini routing.
		{"gemini-4-argon", protocolGoogle, 1048576, 65536, "low,medium,high", true, 2, 10, 0.1, 0},
	}
	for _, c := range cases {
		got := meta[c.id]
		if got.Protocol != c.protocol {
			t.Errorf("%s: protocol %q want %q", c.id, got.Protocol, c.protocol)
		}
		if got.ContextWindow != c.context || got.MaxOutput != c.output {
			t.Errorf("%s: limits %+v", c.id, got)
		}
		if strings.Join(got.ThinkingLevels, ",") != c.levels || got.ThinkingKnown != c.known {
			t.Errorf("%s: levels %v known=%v want %q", c.id, got.ThinkingLevels, got.ThinkingKnown, c.levels)
		}
		if got.PriceInput != c.in || got.PriceOutput != c.out || got.PriceCacheRead != c.read || got.PriceCacheWrite != c.write {
			t.Errorf("%s: prices %+v", c.id, got)
		}
	}
}

// A provider that publishes no metadata must read as "said nothing"
// rather than "no thinking", or every plain provider would lose its chip.
func TestParseCatalogWithoutMetadata(t *testing.T) {
	ids, meta, err := ParseCatalog([]byte(`{"object":"list","data":[{"id":"m1","object":"model"},{"id":"m2"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || meta != nil {
		t.Fatalf("ids %v meta %v", ids, meta)
	}
	// The bare-array form works too, so a non-OpenAI listing parses.
	ids, _, err = ParseCatalog([]byte(`[{"id":"m3"}]`))
	if err != nil || len(ids) != 1 || ids[0] != "m3" {
		t.Fatalf("array form: %v %v", ids, err)
	}
	if _, _, err := ParseCatalog([]byte(`{bad`)); err == nil {
		t.Fatal("corrupt listing must error")
	}
}

// A listing that describes a model's reasoning as unsupported is
// authoritative: the model has no levels, not "unknown".
func TestParseCatalogReasoningUnsupported(t *testing.T) {
	_, meta, err := ParseCatalog([]byte(`{"data":[{"id":"plain","capabilities":{"reasoning":{"supported":false}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := meta["plain"]
	if !got.ThinkingKnown || len(got.ThinkingLevels) != 0 {
		t.Fatalf("got %+v", got)
	}
	if len(ModelEffortLevels(config.ApiTypeOpenai, config.OpencodeBaseURL, "plain", got)) != 0 {
		t.Fatal("an unsupported-reasoning model must offer no levels")
	}
}

// Levels come back weakest first whatever order the listing used, since
// the slider's position is the level.
func TestParseCatalogSortsLevels(t *testing.T) {
	_, meta, err := ParseCatalog([]byte(`{"data":[{"id":"m","capabilities":{"reasoning":{"supported":true,"effort":["max","low","high"]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(meta["m"].ThinkingLevels, ","); got != "low,high,max" {
		t.Fatalf("got %q", got)
	}
	// Unknown levels are dropped instead of offered and then rejected.
	_, meta, err = ParseCatalog([]byte(`{"data":[{"id":"m","capabilities":{"reasoning":{"supported":true,"effort":["low","turbo"]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(meta["m"].ThinkingLevels, ","); got != "low" {
		t.Fatalf("got %q", got)
	}
}
