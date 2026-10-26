//go:build linux

package config

import "testing"

// The search key must stay identical to what keyring-rs uses, otherwise
// keys from other clients become invisible.
func TestKeyStoreIdentityAttributes(t *testing.T) {
	attrs := identityAttributes("my provider")
	want := map[string]string{
		"service":  KeyringService,
		"username": "my provider",
		"target":   "default",
	}
	if len(attrs) != len(want) {
		t.Fatalf("identity attributes = %v, want %v", attrs, want)
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("identity attribute %q = %q, want %q", k, attrs[k], v)
		}
	}
	if _, ok := attrs["application"]; ok {
		t.Error("application must not be part of the search identity")
	}

	legacy := legacyAttributes("my provider")
	if _, ok := legacy["target"]; ok {
		t.Error("legacy attributes must not constrain target")
	}
	if legacy["service"] != KeyringService || legacy["username"] != "my provider" {
		t.Errorf("legacy attributes = %v", legacy)
	}

	created := createdAttributes("my provider")
	for _, k := range []string{"service", "username", "target", "application", "xdg:schema"} {
		if created[k] == "" {
			t.Errorf("created attributes missing %q: %v", k, created)
		}
	}
	if created["target"] != "default" {
		t.Errorf("created target = %q, want default", created["target"])
	}
	if created["username"] != "my provider" {
		t.Errorf("created username = %q", created["username"])
	}
}
