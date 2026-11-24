package claude

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLiveHaikuProtocol(t *testing.T) {
	if os.Getenv("PALADIN_CLAUDE_LIVE") != "1" {
		t.Skip("set PALADIN_CLAUDE_LIVE=1 for billed Haiku 5.5 protocol tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := Start(ctx, Options{Cwd: t.TempDir(), SessionID: uuid.NewString(), Model: "claude-haiku-5-5", PermissionMode: "default", MaxBudget: 0.15}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Prompt("Reply with exactly: PALADIN_CLAUDE_OK. Do not use any tools.", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	stream, assistant, result := false, false, false
	for !result {
		for {
			r, ok := c.sub.Next()
			if !ok {
				break
			}
			t.Logf("frame type=%s subtype=%s messageID=%s", r.Str("type"), r.Str("subtype"), r.Obj("message").Str("id"))
			if r.Str("type") == "assistant" {
				assistant = true
				if m := r.Obj("message").Str("model"); m != "claude-haiku-5-5" {
					t.Fatalf("unexpected live model %q", m)
				}
			}
			if r.Str("type") == "stream_event" {
				stream = true
			}
			if r.Str("type") == "result" {
				if r.Bool("is_error") {
					t.Fatalf("Haiku result failed: %s", r.Str("result"))
				}
				result = true
				break
			}
		}
		if result {
			break
		}
		select {
		case <-c.sub.Wake():
		case <-c.Done():
			t.Fatalf("process exited before result: %v", c.Err())
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if !stream || !assistant {
		t.Fatalf("missing events: partial=%v assistant=%v", stream, assistant)
	}
}
