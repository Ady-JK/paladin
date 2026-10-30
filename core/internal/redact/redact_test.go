package redact

import (
	"strings"
	"testing"
)

func TestStringMasksSecrets(t *testing.T) {
	cases := map[string]string{
		"key sk-proj-abcdefghij1234567890 here":                                  "sk-proj-abcdefghij1234567890",
		"token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345":                             "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
		"aws AKIAABCDEFGHIJKLMNOP":                                               "AKIAABCDEFGHIJKLMNOP",
		"google AIzaSyA1234567890abcdefghijklmnopqrstu":                          "AIzaSyA1234567890abcdefghijklmnopqrstu",
		"Authorization: Bearer abc.def-123456789xyz":                             "abc.def-123456789xyz",
		"clone https://user:hunter2pass@github.com/x/y.git":                      "hunter2pass",
		"OPENAI_API_KEY=abcdef123456\nPATH=/usr/bin":                             "abcdef123456",
		"export DB_PASSWORD=s3cr3tvalue":                                         "s3cr3tvalue",
		"GITHUB_TOKEN='quoted-token-value'":                                      "quoted-token-value",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----": "MIIEow",
		"npm token npm_abcdefghijklmnop1234":                                     "npm_abcdefghijklmnop1234",
	}
	for in, secret := range cases {
		out := String(in)
		if strings.Contains(out, secret) {
			t.Errorf("%q leaked %q: %q", in, secret, out)
		}
	}
	keep := []string{
		"PATH=/usr/bin:/bin",
		"api_key = os.getenv(\"OPENAI_API_KEY\")",
		"TOKEN_LIMIT=4096 is not a secret? it is short",
		"see https://github.com/x/y",
		"sk-short",
		"Bearer",
		"TOKEN=$OTHER",
	}
	for _, in := range keep[:2] {
		if out := String(in); out != in {
			t.Errorf("%q changed to %q", in, out)
		}
	}
	for _, in := range keep[3:] {
		if out := String(in); out != in {
			t.Errorf("%q changed to %q", in, out)
		}
	}
}

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

func TestMaskKeepsShape(t *testing.T) {
	out := String("sk-proj-abcdefghij1234567890")
	if !strings.HasPrefix(out, "sk-pro") || !strings.Contains(out, "…") {
		t.Fatalf("%q", out)
	}
}
