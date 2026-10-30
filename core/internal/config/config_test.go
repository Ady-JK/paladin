package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(dir, NewMemoryKeyStore()), dir
}

func TestPromptFileSeedsSavesResets(t *testing.T) {
	s, dir := testStore(t)
	// A missing file seeds from the built-in default.
	if got := s.LoadSystemPrompt(); got == "" || strings.TrimSpace(got) == "" {
		t.Fatal("empty system prompt")
	}
	if _, err := os.Stat(filepath.Join(dir, "config", SystemPromptFileName)); err != nil {
		t.Fatalf("not seeded: %v", err)
	}
	// Saved text round-trips verbatim.
	if err := s.SaveSystemPrompt("custom sys"); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadSystemPrompt(); got != "custom sys" {
		t.Fatalf("got %q", got)
	}
	// A blank save is rejected so the UI cannot wipe the prompt.
	if err := s.SaveSystemPrompt("  "); err == nil {
		t.Fatal("blank system prompt accepted")
	}
	// Reset restores the built-in default.
	if err := s.ResetPrompt(); err != nil {
		t.Fatal(err)
	}
	if got := s.LoadSystemPrompt(); got != DefaultSystemPrompt() {
		t.Fatal("reset did not restore system prompt")
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

// ---- merged from config_extra_test.go ----

func TestDataDirHonoursOverride(t *testing.T) {
	t.Setenv("PALADIN_DATA_DIR", "  /tmp/custom  ")
	if got := DataDir(); got != "/tmp/custom" {
		t.Fatalf("got %q", got)
	}
}

func TestParseApiTypeVariants(t *testing.T) {
	for in, want := range map[string]ApiType{
		"openai": "openai", " OpenAI ": "openai",
		"ANTHROPIC": "anthropic", "google": "google",
	} {
		got, err := ParseApiType(in)
		if err != nil || got != want {
			t.Fatalf("ParseApiType(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseApiType("azure"); err == nil {
		t.Fatal("expected error for azure")
	}
	if _, err := ParseApiType(""); err == nil {
		t.Fatal("expected error for empty")
	}
}

func TestValidateProviders(t *testing.T) {
	if err := ValidateProviders([]Provider{{Name: "a"}, {Name: "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProviders([]Provider{{Name: "  "}}); err == nil {
		t.Fatal("expected empty name error")
	}
	if err := ValidateProviders([]Provider{{Name: "a"}, {Name: "a"}}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestValidateProviderInput(t *testing.T) {
	// New providers need scheme, key and known type.
	if _, _, _, err := ValidateProviderInput(ProviderInput{Name: "x", ApiURL: "https://x/v1", ApiKey: "k", ApiType: "openai"}, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []ProviderInput{
		{Name: "", ApiURL: "https://x/v1", ApiKey: "k", ApiType: "openai"},
		{Name: "a/b", ApiURL: "https://x/v1", ApiKey: "k", ApiType: "openai"},
		{Name: "a\tb", ApiURL: "https://x/v1", ApiKey: "k", ApiType: "openai"},
		{Name: "x", ApiURL: "", ApiKey: "k", ApiType: "openai"},
		{Name: "x", ApiURL: "ftp://x", ApiKey: "k", ApiType: "openai"},
		{Name: "x", ApiURL: "https://x/v1", ApiKey: "", ApiType: "openai"},
		{Name: "x", ApiURL: "https://x/v1", ApiKey: "k", ApiType: "azure"},
	} {
		if _, _, _, err := ValidateProviderInput(tc, true); err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
	}
	// Updates may keep the stored key with an empty ApiKey.
	if _, _, _, err := ValidateProviderInput(ProviderInput{Name: "x", ApiURL: "https://x/v1", ApiType: "openai"}, false); err != nil {
		t.Fatalf("update without key: %v", err)
	}
	name, url, at, err := ValidateProviderInput(ProviderInput{Name: "  MyProv  ", ApiURL: "https://x/v1/", ApiKey: "k", ApiType: "OpenAI"}, true)
	if err != nil || name != "MyProv" || url != "https://x/v1/" || at != ApiTypeOpenai {
		t.Fatalf("got %q %q %q %v", name, url, at, err)
	}
}

func TestEnsureSeededKeepsExisting(t *testing.T) {
	file := EnsureSeeded(ProvidersFile{
		DefaultProvider: "custom",
		Providers:       []Provider{{Name: "custom", ApiURL: "https://c/v1", ApiType: ApiTypeOpenai}},
	})
	if file.DefaultProvider != "custom" {
		t.Fatalf("default overwritten: %q", file.DefaultProvider)
	}
	seen := map[string]bool{}
	for _, p := range file.Providers {
		seen[p.Name] = true
	}
	for _, want := range []string{"custom", DefaultProviderName, OpenrouterName} {
		if !seen[want] {
			t.Fatalf("missing %q in %v", want, seen)
		}
	}
	// Idempotent: seeding twice adds nothing.
	again := EnsureSeeded(file)
	if len(again.Providers) != len(file.Providers) {
		t.Fatalf("not idempotent: %d vs %d", len(file.Providers), len(again.Providers))
	}
	// Empty default is repaired.
	empty := EnsureSeeded(ProvidersFile{})
	if empty.DefaultProvider != DefaultProviderName {
		t.Fatalf("got %q", empty.DefaultProvider)
	}
}

func TestIsPlaceholderKeyMatrix(t *testing.T) {
	for _, k := range []string{"", "replace_me", "REPLACE-ME", "changeme123", "dummy", "YOUR-API-KEY", "sk-example-1", "abxxxcd", "sk-live-abc123"} {
		placeholder := k == "" || strings.Contains(strings.ToLower(k), "replace") ||
			strings.Contains(strings.ToLower(k), "changeme") || strings.Contains(strings.ToLower(k), "dummy") ||
			strings.Contains(strings.ToLower(k), "your-api-key") || strings.Contains(strings.ToLower(k), "example") ||
			strings.Contains(strings.ToLower(k), "xxx")
		if got := IsPlaceholderKey(k); got != placeholder {
			t.Errorf("IsPlaceholderKey(%q) = %v, want %v", k, got, placeholder)
		}
	}
}

func TestIsOpencodeEndpoint(t *testing.T) {
	for _, url := range []string{
		"https://opencode.ai/inference/go/openai/v1",
		"HTTPS://OPencode.AI/INFERENCE/x",
		"https://opencode.ai/zen/go/v1",
		"https://go.opencode.ai/openai/v1",
		" https://opencode.ai ",
	} {
		if !IsOpencodeEndpoint(url) {
			t.Fatalf("%q not recognised", url)
		}
	}
	for _, url := range []string{
		"https://api.openai.com/v1",
		"https://opencode.ai.example.com/v1",
		"https://evilopencode.ai/v1",
		"",
	} {
		if IsOpencodeEndpoint(url) {
			t.Fatalf("%q wrongly matched", url)
		}
	}
}

func TestAtomicWriteRoundTripAndPerms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "providers.json")
	if err := AtomicWrite(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("got %q %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm()&0o700 != 0o700 {
		t.Fatalf("dir perm %o", dirInfo.Mode().Perm())
	}
	// No stray temp files.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestLoadProvidersRejectsBadJSON(t *testing.T) {
	store, _ := testStore(t)
	if err := os.MkdirAll(filepath.Dir(store.ProvidersPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ProvidersPath(), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadProviders(); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadProvidersRejectsDuplicateNames(t *testing.T) {
	store, _ := testStore(t)
	if err := os.MkdirAll(filepath.Dir(store.ProvidersPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	dup := `{"defaultProvider":"a","providers":[{"name":"a","apiUrl":"https://a/v1","apiType":"openai"},{"name":"a","apiUrl":"https://b/v1","apiType":"openai"}]}`
	if err := os.WriteFile(store.ProvidersPath(), []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadProviders(); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestLoadProvidersAddsMissingSeeds(t *testing.T) {
	store, _ := testStore(t)
	file, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	// Drop openrouter, reload: it must be re-seeded and persisted.
	kept := file.Providers[:0]
	for _, p := range file.Providers {
		if p.Name != OpenrouterName {
			kept = append(kept, p)
		}
	}
	file.Providers = kept
	if err := store.SaveProviders(file); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range reloaded.Providers {
		if p.Name == OpenrouterName {
			found = true
		}
	}
	if !found {
		t.Fatal("openrouter was not re-seeded")
	}
}

func TestModelsRoundTrip(t *testing.T) {
	store, _ := testStore(t)
	file := ModelsFile{Providers: map[string]ProviderModels{
		"openrouter": {AllModels: []string{"b", "a"}, EnabledModels: []string{"a"}},
	}}
	if err := store.SaveModels(file); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers["openrouter"].AllModels) != 2 {
		t.Fatalf("got %+v", loaded)
	}
	// Missing file seeds empty.
	store2, _ := testStore(t)
	empty, err := store2.LoadModels()
	if err != nil || len(empty.Providers) != 0 {
		t.Fatalf("got %+v %v", empty, err)
	}
}

func TestModelsForPrefersEnabled(t *testing.T) {
	store, _ := testStore(t)
	if err := store.SaveModels(ModelsFile{Providers: map[string]ProviderModels{
		"p": {AllModels: []string{"a", "b"}, EnabledModels: []string{"b"}},
		"q": {AllModels: []string{"x"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := store.ModelsFor("p"); len(got) != 1 || got[0] != "b" {
		t.Fatalf("got %v", got)
	}
	if got := store.ModelsFor("q"); len(got) != 1 || got[0] != "x" {
		t.Fatalf("got %v", got)
	}
	if got := store.ModelsFor("missing"); got != nil {
		t.Fatalf("got %v", got)
	}
	// Mutating the result must not corrupt the store.
	got := store.ModelsFor("p")
	got[0] = "MUT"
	if again := store.ModelsFor("p"); again[0] != "b" {
		t.Fatalf("aliasing: %v", again)
	}
}

func TestProviderInfosShapes(t *testing.T) {
	store, _ := testStore(t)
	if _, err := store.LoadProviders(); err != nil {
		t.Fatal(err)
	}
	if err := store.Keys.Set(DefaultProviderName, "sk-real"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveModels(ModelsFile{Providers: map[string]ProviderModels{
		DefaultProviderName: {AllModels: []string{"m1", "m2"}},
	}}); err != nil {
		t.Fatal(err)
	}
	infos := store.ProviderInfos()
	if len(infos) == 0 {
		t.Fatal("no infos")
	}
	for _, info := range infos {
		if info.Models == nil {
			t.Fatalf("%s: models nil, want []", info.Name)
		}
		if info.Name == DefaultProviderName {
			if !info.HasKey || !info.Default || len(info.Models) != 2 {
				t.Fatalf("got %+v", info)
			}
		}
		if info.Name == OpenrouterName && info.HasKey {
			t.Fatalf("openrouter should have no key: %+v", info)
		}
	}
	// Corrupt catalog -> nil infos, never a crash.
	if err := os.WriteFile(store.ProvidersPath(), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if infos := store.ProviderInfos(); infos != nil {
		t.Fatalf("got %v", infos)
	}
}

func TestResolveMatrix(t *testing.T) {
	newSeeded := func(t *testing.T) *Store {
		t.Helper()
		s, _ := testStore(t)
		if _, err := s.LoadProviders(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("explicit name wins even with placeholder default", func(t *testing.T) {
		s := newSeeded(t)
		if err := s.Keys.Set(OpenrouterName, "sk-or"); err != nil {
			t.Fatal(err)
		}
		p, key, err := s.Resolve(OpenrouterName)
		if err != nil || p.Name != OpenrouterName || key != "sk-or" {
			t.Fatalf("got %v %q %v", p, key, err)
		}
	})
	t.Run("unknown explicit name errors", func(t *testing.T) {
		s := newSeeded(t)
		if _, _, err := s.Resolve("nope"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("default with key wins", func(t *testing.T) {
		s := newSeeded(t)
		if err := s.Keys.Set(DefaultProviderName, "sk-real"); err != nil {
			t.Fatal(err)
		}
		p, _, err := s.Resolve("")
		if err != nil || p.Name != DefaultProviderName {
			t.Fatalf("got %v %v", p, err)
		}
	})
	t.Run("falls back to first keyed provider", func(t *testing.T) {
		s := newSeeded(t)
		if err := s.Keys.Set(OpenrouterName, "sk-or"); err != nil {
			t.Fatal(err)
		}
		p, _, err := s.Resolve("")
		if err != nil || p.Name != OpenrouterName {
			t.Fatalf("got %v %v", p, err)
		}
	})
	t.Run("keychain error surfaces", func(t *testing.T) {
		dir := t.TempDir()
		s := NewStore(dir, failingStore{err: os.ErrPermission})
		if _, err := s.LoadProviders(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Resolve(""); err == nil {
			t.Fatal("expected keychain error")
		}
	})
}

type failingStore struct{ err error }

func (f failingStore) Get(string) (string, bool, error) { return "", false, f.err }
func (f failingStore) Set(string, string) error         { return f.err }
func (f failingStore) Delete(string) error              { return f.err }

func TestWorkspaceRoundTrip(t *testing.T) {
	store, _ := testStore(t)
	if got := store.Workspace(); got != "" {
		t.Fatalf("fresh workspace %q", got)
	}
	file, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	file.Workspace = "/tmp/ws"
	if err := store.SaveProviders(file); err != nil {
		t.Fatal(err)
	}
	if got := store.Workspace(); got != "/tmp/ws" {
		t.Fatalf("got %q", got)
	}
}

func TestSaveProvidersRejectsDuplicates(t *testing.T) {
	store, _ := testStore(t)
	dup := ProvidersFile{Providers: []Provider{
		{Name: "a", ApiURL: "https://a/v1", ApiType: ApiTypeOpenai},
		{Name: "a", ApiURL: "https://b/v1", ApiType: ApiTypeOpenai},
	}}
	if err := store.SaveProviders(dup); err == nil {
		t.Fatal("duplicate save accepted")
	}
}

func TestLoadModelsRejectsCorrupt(t *testing.T) {
	store, _ := testStore(t)
	if err := os.MkdirAll(filepath.Dir(store.ModelsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ModelsPath(), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadModels(); err == nil {
		t.Fatal("corrupt models accepted")
	}
	if got := store.ModelsFor("p"); got != nil {
		t.Fatalf("corrupt models should read as nil, got %v", got)
	}
}

func TestResolveFallsBackWhenDefaultMissing(t *testing.T) {
	store, _ := testStore(t)
	file, err := store.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	file.DefaultProvider = "ghost"
	if err := store.SaveProviders(file); err != nil {
		t.Fatal(err)
	}
	if err := store.Keys.Set(OpenrouterName, "sk-or"); err != nil {
		t.Fatal(err)
	}
	p, _, err := store.Resolve("")
	if err != nil || p.Name != OpenrouterName {
		t.Fatalf("got %v %v", p, err)
	}
}

func TestMemoryKeyStoreConcurrent(t *testing.T) {
	m := NewMemoryKeyStore()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Set("p", "k")
			_, _, _ = m.Get("p")
			_ = m.Delete("p")
		}()
	}
	wg.Wait()
}
