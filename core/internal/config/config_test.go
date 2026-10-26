package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(dir, NewMemoryKeyStore()), dir
}

func TestClampMaxToolRounds(t *testing.T) {
	if got := ClampMaxToolRounds(0); got != 100 {
		t.Fatalf("got %d", got)
	}
	if got := ClampMaxToolRounds(999); got != 500 {
		t.Fatalf("got %d", got)
	}
}

func TestIsPlaceholderKey(t *testing.T) {
	if !IsPlaceholderKey("replace_me") || !IsPlaceholderKey("") {
		t.Fatal("expected placeholders")
	}
	if IsPlaceholderKey("sk-live-abc123") {
		t.Fatal("real key flagged as placeholder")
	}
}

func TestSeedsDefaultProvidersOnFirstLoad(t *testing.T) {
	store, _ := testStore(t)
	file, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if file.DefaultProvider != DefaultProviderName {
		t.Fatalf("default %q", file.DefaultProvider)
	}
	found := false
	for _, p := range file.Providers {
		if p.Name == OpenrouterName {
			found = true
		}
	}
	if !found {
		t.Fatal("missing openrouter seed")
	}
	raw, err := os.ReadFile(store.ProvidersPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "api_key") {
		t.Fatal("keys must never land in the JSON file")
	}
}

func TestResolvePrefersDefaultWithRealKey(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.LoadProviders(); err != nil {
		t.Fatal(err)
	}
	if err := store.Keys.Set(DefaultProviderName, "sk-real"); err != nil {
		t.Fatal(err)
	}
	p, key, err := store.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != DefaultProviderName || key != "sk-real" {
		t.Fatalf("got %+v", p)
	}
}

func TestResolveRejectsPlaceholderOnlyCatalog(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.LoadProviders(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Resolve(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	if _, _, _, err := ValidateProviderInput(ProviderInput{Name: "x", ApiURL: "ftp://x", ApiKey: "k", ApiType: "openai"}, true); err == nil {
		t.Fatal("expected url error")
	}
	if _, _, _, err := ValidateProviderInput(ProviderInput{Name: "x", ApiURL: "https://x/v1", ApiKey: "", ApiType: "openai"}, true); err == nil {
		t.Fatal("expected key error")
	}
}

func TestProvidersFileRoundTrip(t *testing.T) {
	store, _ := testStore(t)
	file, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	file.Workspace = "/tmp/ws"
	if err := store.SaveProviders(file); err != nil {
		t.Fatal(err)
	}
	again, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if again.Workspace != "/tmp/ws" {
		t.Fatalf("workspace %q", again.Workspace)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(store.ProvidersPath()))); err != nil {
		t.Fatal(err)
	}
}
