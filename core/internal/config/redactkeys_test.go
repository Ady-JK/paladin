package config

import (
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/redact"
)

func TestStoreKeysAreRedacted(t *testing.T) {
	defer redact.Reset()
	s := NewStore(t.TempDir(), NewMemoryKeyStore())
	if err := s.Keys.Set("custom", "plain-provider-key-123"); err != nil {
		t.Fatal(err)
	}
	if out := redact.String("echo plain-provider-key-123"); strings.Contains(out, "plain-provider-key-123") {
		t.Fatalf("stored key not masked: %q", out)
	}
	if err := s.Keys.Delete("custom"); err != nil {
		t.Fatal(err)
	}
	if out := redact.String("plain-provider-key-123"); out != "plain-provider-key-123" {
		t.Fatalf("deleted key still masked: %q", out)
	}
}
