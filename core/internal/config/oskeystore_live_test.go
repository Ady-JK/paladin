//go:build linux

package config

import (
	"os"
	"testing"
	"time"

	dbus "github.com/godbus/dbus/v5"
	ss "github.com/zalando/go-keyring/secret_service"
)

// These tests talk to the real Secret Service (gnome-keyring / KWallet)
// on the developer's desktop bus, so they only run when explicitly asked:
//
//	PALADIN_KEYRING_LIVE=1 go test ./internal/config -run LiveKeyStore -v
//
// They create items under unique provider names and always remove them.
// CI has no desktop keyring, so the gate keeps `go test ./...` hermetic.

func liveProvider(t *testing.T) string {
	t.Helper()
	if os.Getenv("PALADIN_KEYRING_LIVE") != "1" {
		t.Skip("set PALADIN_KEYRING_LIVE=1 to exercise the real Secret Service")
	}
	return "paladin-live-" + t.Name() + "-" + time.Now().Format("150405.000000")
}

func rawCreate(t *testing.T, provider, key string, attrs map[string]string) {
	t.Helper()
	svc, err := ss.NewSecretService()
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}
	session, err := svc.OpenSession()
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer svc.Close(session)
	collection := defaultCollection(svc)
	if err := svc.Unlock(collection.Path()); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := svc.CreateItem(collection, "paladin live test", attrs, ss.NewSecret(session.Path(), key)); err != nil {
		t.Fatalf("create raw item: %v", err)
	}
}

func rawItems(t *testing.T, provider string) []dbus.ObjectPath {
	t.Helper()
	svc, err := ss.NewSecretService()
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}
	items, err := searchAll(svc, identityAttributes(provider))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return items
}

func rawAttributes(t *testing.T, item dbus.ObjectPath) map[string]string {
	t.Helper()
	svc, err := ss.NewSecretService()
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}
	v, err := svc.Object(secretServiceName, item).GetProperty(itemIface + ".Attributes")
	if err != nil {
		t.Fatalf("attributes: %v", err)
	}
	attrs, ok := v.Value().(map[string]string)
	if !ok {
		t.Fatalf("attributes type %T", v.Value())
	}
	return attrs
}

func TestLiveKeyStoreRoundTrip(t *testing.T) {
	provider := liveProvider(t)
	store := OsKeyStore{}
	t.Cleanup(func() { _ = store.Delete(provider) })

	if _, ok, err := store.Get(provider); err != nil || ok {
		t.Fatalf("empty Get = (%v, %v), want (false, nil)", ok, err)
	}
	if err := store.Set(provider, "sk-first-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := store.Get(provider)
	if err != nil || !ok || got != "sk-first-value" {
		t.Fatalf("Get = (%q, %v, %v)", got, ok, err)
	}
	// Overwrite must update in place, not add a second item.
	if err := store.Set(provider, "sk-second-value"); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	got, ok, err = store.Get(provider)
	if err != nil || !ok || got != "sk-second-value" {
		t.Fatalf("Get after overwrite = (%q, %v, %v)", got, ok, err)
	}
	if items := rawItems(t, provider); len(items) != 1 {
		t.Fatalf("overwrite left %d items, want 1", len(items))
	}
	attrs := rawAttributes(t, rawItems(t, provider)[0])
	want := map[string]string{
		"service":     KeyringService,
		"username":    provider,
		"target":      "default",
		"application": "paladin",
		"xdg:schema":  genericSchema,
	}
	for k, v := range want {
		if attrs[k] != v {
			t.Errorf("attribute %q = %q, want %q (all: %v)", k, attrs[k], v, attrs)
		}
	}
	if err := store.Delete(provider); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := store.Get(provider); err != nil || ok {
		t.Fatalf("Get after Delete = (%v, %v), want (false, nil)", ok, err)
	}
	if err := store.Delete(provider); err != nil {
		t.Fatalf("Delete twice: %v", err)
	}
}

func TestLiveKeyStoreReadsKeyringRSItem(t *testing.T) {
	provider := liveProvider(t)
	store := OsKeyStore{}
	t.Cleanup(func() { _ = store.Delete(provider) })

	// Exactly what keyring-rs v3 writes: application=rust-keyring.
	rawCreate(t, provider, "sk-from-rust", map[string]string{
		"service":     KeyringService,
		"username":    provider,
		"target":      "default",
		"application": "rust-keyring",
	})
	got, ok, err := store.Get(provider)
	if err != nil || !ok || got != "sk-from-rust" {
		t.Fatalf("Get rust-style item = (%q, %v, %v)", got, ok, err)
	}
}

func TestLiveKeyStoreReadsLegacyItem(t *testing.T) {
	provider := liveProvider(t)
	store := OsKeyStore{}
	t.Cleanup(func() { _ = store.Delete(provider) })

	// keyring v1 items have no target attribute at all.
	rawCreate(t, provider, "sk-legacy", map[string]string{
		"service":  KeyringService,
		"username": provider,
	})
	got, ok, err := store.Get(provider)
	if err != nil || !ok || got != "sk-legacy" {
		t.Fatalf("Get legacy item = (%q, %v, %v)", got, ok, err)
	}
}

func TestLiveKeyStoreDedupesOnSet(t *testing.T) {
	provider := liveProvider(t)
	store := OsKeyStore{}
	t.Cleanup(func() { _ = store.Delete(provider) })

	// Two items with the same identity but different application tags:
	// what an older build plus keyring-rs could leave behind.
	rawCreate(t, provider, "sk-old-rust", map[string]string{
		"service":     KeyringService,
		"username":    provider,
		"target":      "default",
		"application": "rust-keyring",
	})
	rawCreate(t, provider, "sk-old-go", map[string]string{
		"service":     KeyringService,
		"username":    provider,
		"target":      "default",
		"application": "paladin",
	})
	if items := rawItems(t, provider); len(items) != 2 {
		t.Fatalf("setup created %d items, want 2", len(items))
	}

	if err := store.Set(provider, "sk-new"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if items := rawItems(t, provider); len(items) != 1 {
		t.Fatalf("Set left %d items, want 1", len(items))
	}
	got, ok, err := store.Get(provider)
	if err != nil || !ok || got != "sk-new" {
		t.Fatalf("Get = (%q, %v, %v)", got, ok, err)
	}
}

func TestLiveKeyStoreUnicodeProvider(t *testing.T) {
	provider := liveProvider(t) + "-Ünïcode Provider (beta)"
	store := OsKeyStore{}
	t.Cleanup(func() { _ = store.Delete(provider) })

	if err := store.Set(provider, "sk-unicode"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := store.Get(provider)
	if err != nil || !ok || got != "sk-unicode" {
		t.Fatalf("Get = (%q, %v, %v)", got, ok, err)
	}
}

func TestLiveKeyStoreStoreResolve(t *testing.T) {
	provider := liveProvider(t)
	keys := OsKeyStore{}
	t.Cleanup(func() { _ = keys.Delete(provider) })

	store := NewStore(t.TempDir(), keys)
	file, err := store.LoadProviders()
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	file.Providers = append(file.Providers, Provider{
		Name:    provider,
		ApiURL:  "https://example.test/v1",
		ApiType: ApiTypeOpenai,
	})
	if err := store.SaveProviders(file); err != nil {
		t.Fatalf("SaveProviders: %v", err)
	}
	if err := keys.Set(provider, "sk-resolve"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// hasKey comes from the keychain on every list.
	var hasKey bool
	for _, info := range store.ProviderInfos() {
		if info.Name == provider {
			hasKey = info.HasKey
		}
	}
	if !hasKey {
		t.Fatalf("ProviderInfos().HasKey = false for %q", provider)
	}
	p, key, err := store.Resolve(provider)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Name != provider || key != "sk-resolve" {
		t.Fatalf("Resolve = (%q, %q), want (%q, %q)", p.Name, key, provider, "sk-resolve")
	}
}
