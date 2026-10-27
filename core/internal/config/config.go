// Package config owns provider catalog persistence, validation, and
// secret storage. Disk layout matches the Rust core exactly:
// config/providers.json, config/models.json, sessions/, logs/.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

const (
	DefaultMaxToolRounds = 100
	MaxMaxToolRounds     = 500

	DefaultProviderName = "Opencode Go"
	OpencodeBaseURL     = "https://opencode.ai/inference/go/openai/v1"
	OpenrouterName      = "openrouter"
	OpenrouterBaseURL   = "https://openrouter.ai/api/v1"
	PlaceholderKey      = "replace_me"

	KeyringService = "paladin"

	OpencodeCatalogURL    = "https://opencode.ai/inference/go/v1/models"
	OpencodeUserAgent     = "paladin/1.0"
	OpencodeSessionHeader = "x-opencode-session"
)

// DataDir resolves the shared data directory:
// $PALADIN_DATA_DIR override, else the OS-specific config dir.
func DataDir() string {
	if dir := strings.TrimSpace(os.Getenv("PALADIN_DATA_DIR")); dir != "" {
		return dir
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "paladin")
	}
	return ".paladin"
}

// ApiType is the provider dialect. Only openai is implemented in v1.
type ApiType string

const (
	ApiTypeOpenai    ApiType = "openai"
	ApiTypeAnthropic ApiType = "anthropic"
	ApiTypeGoogle    ApiType = "google"
)

func ParseApiType(s string) (ApiType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "openai":
		return ApiTypeOpenai, nil
	case "anthropic":
		return ApiTypeAnthropic, nil
	case "google":
		return ApiTypeGoogle, nil
	default:
		return "", fmt.Errorf("unknown api_type %q (want one of openai, anthropic, google)", s)
	}
}

// Provider is one on-disk catalog entry. The API key is never stored
// here; it lives in the OS keychain behind KeyStore.
type Provider struct {
	Name    string  `json:"name"`
	ApiURL  string  `json:"api_url"`
	ApiType ApiType `json:"api_type"`
	Model   string  `json:"model"`
}

type ProviderInfo struct {
	ApiType ApiType  `json:"apiType"`
	ApiURL  string   `json:"apiUrl"`
	Default bool     `json:"default"`
	HasKey  bool     `json:"hasKey"`
	Model   string   `json:"model"`
	Models  []string `json:"models"`
	Name    string   `json:"name"`
}

type ProviderInput struct {
	Name    string `json:"name"`
	ApiURL  string `json:"apiUrl"`
	ApiKey  string `json:"apiKey"`
	ApiType string `json:"apiType"`
}

type ProvidersFile struct {
	DefaultProvider string     `json:"default_provider"`
	Providers       []Provider `json:"providers"`
	Workspace       string     `json:"workspace"`
}

type ProviderModels struct {
	AllModels     []string `json:"all_models"`
	EnabledModels []string `json:"enabled_models"`
}

type ModelsFile struct {
	Providers map[string]ProviderModels `json:"providers"`
}

// KeyStore is secret storage for provider API keys.
type KeyStore interface {
	Get(provider string) (string, bool, error)
	Set(provider, key string) error
	Delete(provider string) error
}

// MemoryKeyStore is an in-memory key store for tests.
type MemoryKeyStore struct {
	mu   sync.Mutex
	keys map[string]string
}

func NewMemoryKeyStore() *MemoryKeyStore {
	return &MemoryKeyStore{keys: map[string]string{}}
}

func (m *MemoryKeyStore) Get(provider string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.keys[provider]
	return key, ok, nil
}

func (m *MemoryKeyStore) Set(provider, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[provider] = key
	return nil
}

func (m *MemoryKeyStore) Delete(provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, provider)
	return nil
}

// Store is the on-disk catalog: config/providers.json +
// config/models.json under the data dir, with secrets in the KeyStore.
type Store struct {
	dataDir string
	Keys    KeyStore
}

func NewStore(dataDir string, keys KeyStore) *Store {
	return &Store{dataDir: dataDir, Keys: keys}
}

func (s *Store) ProvidersPath() string {
	return filepath.Join(s.dataDir, "config", "providers.json")
}

func (s *Store) ModelsPath() string {
	return filepath.Join(s.dataDir, "config", "models.json")
}

func (s *Store) LoadProviders() (ProvidersFile, error) {
	path := s.ProvidersPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			seeded := EnsureSeeded(DefaultProvidersFile())
			if err := s.SaveProviders(seeded); err != nil {
				return ProvidersFile{}, err
			}
			return seeded, nil
		}
		return ProvidersFile{}, fmt.Errorf("io: %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return ProvidersFile{}, nil
	}
	var file ProvidersFile
	if err := json.Unmarshal(data, &file); err != nil {
		return ProvidersFile{}, fmt.Errorf("parse: %s: %w", path, err)
	}
	if err := ValidateProviders(file.Providers); err != nil {
		return ProvidersFile{}, err
	}
	before := len(file.Providers)
	beforeDefault := file.DefaultProvider
	file = EnsureSeeded(file)
	if len(file.Providers) != before || file.DefaultProvider != beforeDefault {
		if err := s.SaveProviders(file); err != nil {
			return ProvidersFile{}, err
		}
	}
	return file, nil
}

func (s *Store) SaveProviders(file ProvidersFile) error {
	if err := ValidateProviders(file.Providers); err != nil {
		return err
	}
	data, err := jsonx.MarshalIndent(file)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	return AtomicWrite(s.ProvidersPath(), data)
}

func (s *Store) LoadModels() (ModelsFile, error) {
	path := s.ModelsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			empty := ModelsFile{}
			if err := s.SaveModels(empty); err != nil {
				return ModelsFile{}, err
			}
			return empty, nil
		}
		return ModelsFile{}, fmt.Errorf("io: %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return ModelsFile{}, nil
	}
	var file ModelsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return ModelsFile{}, fmt.Errorf("parse: %s: %w", path, err)
	}
	if file.Providers == nil {
		file.Providers = map[string]ProviderModels{}
	}
	return file, nil
}

func (s *Store) SaveModels(file ModelsFile) error {
	if file.Providers == nil {
		file.Providers = map[string]ProviderModels{}
	}
	data, err := jsonx.MarshalIndent(file)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	return AtomicWrite(s.ModelsPath(), data)
}

// Resolve the provider to use: explicit name wins, else the default
// when it has a real key, else the first provider with a real key.
// Returns the entry plus its keychain key (empty when unset).
func (s *Store) Resolve(name string) (Provider, string, error) {
	file, err := s.LoadProviders()
	if err != nil {
		return Provider{}, "", err
	}
	if strings.TrimSpace(name) != "" {
		for _, p := range file.Providers {
			if p.Name == name {
				key, _, err := s.Keys.Get(p.Name)
				if err != nil {
					return Provider{}, "", err
				}
				return p, key, nil
			}
		}
		return Provider{}, "", fmt.Errorf("parse: provider %q not found", name)
	}
	if file.DefaultProvider != "" {
		for _, p := range file.Providers {
			if p.Name == file.DefaultProvider {
				key, _, err := s.Keys.Get(p.Name)
				if err != nil {
					return Provider{}, "", err
				}
				if !IsPlaceholderKey(key) {
					return p, key, nil
				}
				for _, q := range file.Providers {
					qkey, _, err := s.Keys.Get(q.Name)
					if err != nil {
						return Provider{}, "", err
					}
					if !IsPlaceholderKey(qkey) {
						return q, qkey, nil
					}
				}
				return Provider{}, "", fmt.Errorf("parse: no usable provider: %q still has a placeholder api key; add one in settings", p.Name)
			}
		}
	}
	for _, p := range file.Providers {
		key, _, err := s.Keys.Get(p.Name)
		if err != nil {
			return Provider{}, "", err
		}
		if !IsPlaceholderKey(key) {
			return p, key, nil
		}
	}
	if len(file.Providers) > 0 {
		return Provider{}, "", fmt.Errorf("parse: no usable provider: %q still has a placeholder api key; add one in settings", file.Providers[0].Name)
	}
	return Provider{}, "", fmt.Errorf("parse: no providers configured")
}

func (s *Store) ModelsFor(name string) []string {
	file, err := s.LoadModels()
	if err != nil {
		return nil
	}
	if entry, ok := file.Providers[name]; ok {
		if len(entry.EnabledModels) > 0 {
			return append([]string(nil), entry.EnabledModels...)
		}
		return append([]string(nil), entry.AllModels...)
	}
	return nil
}

func (s *Store) ProviderInfos() []ProviderInfo {
	file, err := s.LoadProviders()
	if err != nil {
		return nil
	}
	models, err := s.LoadModels()
	if err != nil {
		models = ModelsFile{}
	}
	var out []ProviderInfo
	for _, p := range file.Providers {
		var list []string
		if entry, ok := models.Providers[p.Name]; ok {
			if len(entry.EnabledModels) > 0 {
				list = append([]string(nil), entry.EnabledModels...)
			} else {
				list = append([]string(nil), entry.AllModels...)
			}
		}
		if list == nil {
			list = []string{}
		}
		key, _, _ := s.Keys.Get(p.Name)
		out = append(out, ProviderInfo{
			Name:    p.Name,
			ApiType: p.ApiType,
			ApiURL:  p.ApiURL,
			Model:   p.Model,
			Models:  list,
			Default: p.Name == file.DefaultProvider,
			HasKey:  !IsPlaceholderKey(key),
		})
	}
	if out == nil {
		out = []ProviderInfo{}
	}
	return out
}

func (s *Store) Workspace() string {
	file, err := s.LoadProviders()
	if err != nil {
		return ""
	}
	return file.Workspace
}

func DefaultProvidersFile() ProvidersFile {
	return ProvidersFile{
		DefaultProvider: DefaultProviderName,
		Providers: []Provider{
			{Name: DefaultProviderName, ApiURL: OpencodeBaseURL, ApiType: ApiTypeOpenai},
			{Name: OpenrouterName, ApiURL: OpenrouterBaseURL, ApiType: ApiTypeOpenai},
		},
	}
}

// EnsureSeeded adds the shipped entries when missing, leaving existing
// entries and the default untouched.
func EnsureSeeded(file ProvidersFile) ProvidersFile {
	for _, seed := range DefaultProvidersFile().Providers {
		found := false
		for _, p := range file.Providers {
			if p.Name == seed.Name {
				found = true
				break
			}
		}
		if !found {
			file.Providers = append(file.Providers, seed)
		}
	}
	if file.DefaultProvider == "" {
		file.DefaultProvider = DefaultProviderName
	}
	return file
}

func ValidateProviders(providers []Provider) error {
	seen := map[string]bool{}
	for _, p := range providers {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("parse: provider name must not be empty")
		}
		if seen[p.Name] {
			return fmt.Errorf("parse: duplicate provider name %q", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

func ValidateProviderInput(input ProviderInput, isNew bool) (name, url string, apiType ApiType, err error) {
	name = strings.TrimSpace(input.Name)
	if name == "" {
		return "", "", "", fmt.Errorf("parse: provider name is required")
	}
	if strings.ContainsAny(name, "\t\n/\\") {
		return "", "", "", fmt.Errorf("parse: provider name must be one word")
	}
	url = strings.TrimSpace(input.ApiURL)
	if url == "" {
		return "", "", "", fmt.Errorf("parse: api url is required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", "", "", fmt.Errorf("parse: api url must start with http:// or https://")
	}
	if isNew && strings.TrimSpace(input.ApiKey) == "" {
		return "", "", "", fmt.Errorf("parse: api key is required")
	}
	apiType, err = ParseApiType(input.ApiType)
	if err != nil {
		return "", "", "", err
	}
	return name, url, apiType, nil
}

func ClampMaxToolRounds(value uint32) uint32 {
	if value == 0 {
		return DefaultMaxToolRounds
	}
	if value > MaxMaxToolRounds {
		return MaxMaxToolRounds
	}
	return value
}

func IsPlaceholderKey(key string) bool {
	lower := strings.ToLower(key)
	return key == "" ||
		strings.Contains(lower, "replace") ||
		strings.Contains(lower, "changeme") ||
		strings.Contains(lower, "dummy") ||
		strings.Contains(lower, "your-api-key") ||
		strings.Contains(lower, "example") ||
		strings.Contains(lower, "xxx")
}

// IsOpencodeEndpoint reports whether apiURL points at OpenCode Go. The
// documented hosts are opencode.ai/inference/... and opencode.ai/zen/...;
// matching the host (not a path prefix) keeps both working, including any
// future gateway path. A lookalike host such as opencode.ai.example.com
// must not opt into OpenCode's session headers.
func IsOpencodeEndpoint(apiURL string) bool {
	trimmed := strings.TrimSpace(apiURL)
	u, err := url.Parse(trimmed)
	if err == nil && u.Hostname() != "" {
		host := strings.ToLower(u.Hostname())
		return host == "opencode.ai" || strings.HasSuffix(host, ".opencode.ai")
	}
	return strings.Contains(strings.ToLower(trimmed), "opencode.ai")
}

// AtomicWrite writes data to path via a temp file + rename, with
// 0700 dirs and 0600 files on unix like the Rust core.
func AtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("io: %s: %s", dir, syserr.Str(err))
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("io: temp file in %s: %s", dir, syserr.Str(err))
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("io: %s: %s", path, syserr.Str(err))
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("io: %s: %s", path, syserr.Str(err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("io: %s: %s", path, syserr.Str(err))
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("io: %s: %s", path, syserr.Str(err))
	}
	return nil
}

// SortedKeys returns sorted provider names; used for deterministic output.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Config mirrors the Rust core Config.
type Config struct {
	DataDir       string
	MaxToolRounds uint32
	SystemPrompt  string
}

func NewConfig(dataDir string) Config {
	return Config{DataDir: dataDir}
}
