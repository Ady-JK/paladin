package runtime

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func TestMergeMetaPreservesLastKnownPricing(t *testing.T) {
	price := &config.ModelPricing{Source: "litellm", FetchedAt: 123}
	got := mergeMeta(map[string]config.ModelMeta{"luna": {Pricing: price}}, map[string]config.ModelMeta{"luna": {Protocol: "responses"}}, []string{"luna"})
	if got["luna"].Pricing != price || got["luna"].Protocol != "responses" {
		t.Fatalf("merged metadata: %+v", got)
	}
}
