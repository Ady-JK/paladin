package redact

import (
	"strings"
	"testing"
)

func TestRegisteredValues(t *testing.T) {
	defer Reset()
	Register("short")
	Register("my-custom-provider-secret-42")
	out := String("got my-custom-provider-secret-42 and short")
	if strings.Contains(out, "my-custom-provider-secret-42") || !strings.Contains(out, "short") {
		t.Fatalf("%q", out)
	}
	Unregister("my-custom-provider-secret-42")
	if out := String("my-custom-provider-secret-42"); out != "my-custom-provider-secret-42" {
		t.Fatalf("unregister: %q", out)
	}
}

// Pattern-shaped secrets are the agent's responsibility now: the harness
// does not scan every output for them.
func TestUnregisteredSecretsAreNotScanned(t *testing.T) {
	defer Reset()
	for _, in := range []string{
		"key sk-proj-abcdefghij1234567890 here",
		"Authorization: Bearer abc.def-123456789xyz",
		"OPENAI_API_KEY=abcdef123456",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----",
	} {
		if out := String(in); out != in {
			t.Fatalf("%q changed to %q", in, out)
		}
	}
}

func TestLongerRegisteredValueWins(t *testing.T) {
	defer Reset()
	Register("abcdefgh")
	Register("abcdefghijkl")
	out := String("x abcdefghijkl y")
	if strings.Contains(out, "abcdefghijkl") {
		t.Fatalf("%q", out)
	}
}
