package inference

import (
	"os"
	"testing"
)

// TestParseLiveCatalogShape runs the parser over a saved copy of the real
// /inference/go/v1/models body, so the shapes the parser reads stay the
// ones the gateway sends. It is skipped when the sample is absent, which
// keeps the suite hermetic; refresh it with
//
//	curl -s https://opencode.ai/inference/go/v1/models > testdata/opencode-models.json
func TestParseLiveCatalogShape(t *testing.T) {
	body, err := os.ReadFile("testdata/opencode-models.json")
	if err != nil {
		t.Skip("no saved catalog sample")
	}
	ids, meta, err := ParseCatalog(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) < 20 {
		t.Fatalf("only %d models", len(ids))
	}
	// Every model the listing describes must be routable and measurable:
	// that is what makes the metadata worth storing.
	described := 0
	for _, id := range ids {
		m, ok := meta[id]
		if !ok {
			continue
		}
		described++
		if m.Protocol == "" {
			t.Errorf("%s: no protocol", id)
		}
		if m.ContextWindow <= 0 {
			t.Errorf("%s: no context window", id)
		}
	}
	if described < len(ids) {
		t.Errorf("%d of %d models described: %d have no metadata", described, len(ids), len(ids)-described)
	}

	rank := map[string]int{EffortNone: 0, EffortMinimal: 1, EffortLow: 2, EffortMedium: 3, EffortHigh: 4, EffortXHigh: 5, EffortMax: 6}
	for id, m := range meta {
		// The chip must never name a level it cannot show, or offer one
		// out of order (the slider's position is the level).
		last := -1
		for _, level := range m.ThinkingLevels {
			r, ok := rank[level]
			if !ok {
				t.Errorf("%s: unknown level %q", id, level)
			}
			if r < last {
				t.Errorf("%s: levels out of order %v", id, m.ThinkingLevels)
			}
			last = r
		}
		// Levels are only offered where the request can carry an effort.
		levels := ModelEffortLevels("openai", "https://opencode.ai/inference/go/openai/v1", id, m)
		if len(levels) > 0 && m.Protocol != protocolChat && m.Protocol != protocolResponses {
			t.Errorf("%s: %s model offered %v", id, m.Protocol, levels)
		}
		if len(m.ThinkingLevels) > 0 && len(levels) == 0 && (m.Protocol == protocolChat || m.Protocol == protocolResponses) {
			t.Errorf("%s: catalog levels dropped for %s", id, m.Protocol)
		}
	}
}
