package inference

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// This opt-in test uses the locally signed-in Copilot account and sends
// synthetic prompts only to the two inexpensive model families requested.
func TestCopilotLiveCatalogAndLowCostModels(t *testing.T) {
	if os.Getenv("PALADIN_COPILOT_LIVE_TEST") != "1" {
		t.Skip("set PALADIN_COPILOT_LIVE_TEST=1 to test the signed-in Copilot account")
	}
	raw, err := os.ReadFile(filepath.Join(config.DataDir(), "config", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings config.ProvidersFile
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	baseURL := ""
	for _, p := range settings.Providers {
		if p.ApiType == config.ApiTypeCopilot && p.Name == "github-copilot" {
			baseURL = p.ApiURL
		}
	}
	if !strings.HasPrefix(baseURL, "https://api.") || !strings.HasSuffix(baseURL, ".githubcopilot.com") {
		t.Fatal("no trusted Copilot endpoint")
	}
	key, ok, err := (config.OsKeyStore{}).Get("github-copilot")
	if err != nil || !ok || key == "" {
		t.Fatal("Copilot sign-in is unavailable")
	}
	provider, err := BuildProvider(config.ApiTypeCopilot, "github-copilot", "", baseURL, key)
	if err != nil {
		t.Fatal(err)
	}
	ids, meta, err := provider.(*OpenAIProvider).ListCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if meta[id].Protocol == "" {
			t.Errorf("missing route for %s", id)
		}
	}
	t.Logf("verified catalog routes for %d selectable models", len(ids))
	for _, c := range []struct{ model, protocol string }{{"claude-haiku-4.5", protocolChat}, {"gpt-5.6-luna", protocolResponses}} {
		t.Run(c.model, func(t *testing.T) {
			metadata, ok := meta[c.model]
			if !ok {
				t.Skip("model is unavailable or disabled")
			}
			if metadata.Protocol != c.protocol {
				t.Fatalf("route %q", metadata.Protocol)
			}
			p, err := BuildProviderWithMeta(config.ApiTypeCopilot, "github-copilot", c.model, baseURL, key, metadata)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			messages := []LlmMessage{{Role: "user", Content: "Reply with exactly: hello"}}
			reply, err := p.CompleteTools(ctx, "", messages, nil, "copilot-live")
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(strings.ToLower(reply.Content)) != "hello" {
				t.Fatalf("unexpected answer %q", reply.Content)
			}
			chunks := make(chan Chunk, 128)
			done := make(chan struct{})
			err = p.StreamTools(ctx, "", messages, nil, "copilot-live", chunks, done)
			close(chunks)
			close(done)
			if err != nil {
				t.Fatal(err)
			}
			var streamed strings.Builder
			for chunk := range chunks {
				if chunk.Err != "" {
					t.Fatalf("stream: %s", chunk.Err)
				}
				streamed.WriteString(chunk.Text)
			}
			if strings.TrimSpace(strings.ToLower(streamed.String())) != "hello" {
				t.Fatalf("unexpected streamed answer %q", streamed.String())
			}
			if c.protocol == protocolResponses && metadata.ThinkingKnown {
				p, err := WithEffort(p, "low")
				if err != nil {
					t.Fatal(err)
				}
				chunks := make(chan Chunk, 128)
				done := make(chan struct{})
				err = p.StreamTools(ctx, "", messages, nil, "copilot-live", chunks, done)
				close(chunks)
				close(done)
				if err != nil {
					t.Fatal(err)
				}
				var text strings.Builder
				for chunk := range chunks {
					if chunk.EffortDropped != "" {
						t.Fatalf("model rejected reasoning level %q", chunk.EffortDropped)
					}
					text.WriteString(chunk.Text)
				}
				if strings.TrimSpace(strings.ToLower(text.String())) != "hello" {
					t.Fatalf("unexpected reasoning answer %q", text.String())
				}
			}
		})
	}
}
