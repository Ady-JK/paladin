// Provider registry, resolution and the per-(provider, model) client cache.

package runtime

import (
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
)

// runtimeProviders adapts provider resolution to agent.ProviderSource.
type runtimeProviders struct{ r *AgentRuntime }

func (p runtimeProviders) ResolveProvider(name, model string) (inference.Provider, error) {
	_, _, provider, err := p.r.resolveProvider(name, model)
	return provider, err
}

func (r *AgentRuntime) evictProviderCache() {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	r.providerCache = map[string]inference.Provider{}
}

func (r *AgentRuntime) resolveProvider(nameOverride, modelOverride string) (config.Provider, string, inference.Provider, error) {
	entry, key, err := r.store.Resolve(nameOverride)
	if err != nil {
		return config.Provider{}, "", nil, err
	}
	if strings.TrimSpace(modelOverride) != "" {
		entry.Model = strings.TrimSpace(modelOverride)
	}
	if strings.TrimSpace(entry.Model) == "" {
		entry.Model = r.defaultModel(entry)
	}
	cacheKey := entry.Name + "\x00" + entry.Model
	r.cacheMu.Lock()
	if cached, ok := r.providerCache[cacheKey]; ok {
		r.cacheMu.Unlock()
		return entry, key, cached, nil
	}
	r.cacheMu.Unlock()
	provider, err := inference.BuildProvider(entry.ApiType, entry.Name, entry.Model, entry.ApiURL, key)
	if err != nil {
		return config.Provider{}, "", nil, err
	}
	r.cacheMu.Lock()
	r.providerCache[cacheKey] = provider
	r.cacheMu.Unlock()
	return entry, key, provider, nil
}

// defaultModel picks the model a session should use when the caller did
// not choose one. Prefer the provider's fetched catalog so a new session
// never falls back to the generic gpt-4o-mini default on a provider that
// does not serve it. OpenCode additionally prefers a known chat model
// when the catalog is empty, because an invalid model name is rejected
// before the request reaches the subscription.
func (r *AgentRuntime) defaultModel(entry config.Provider) string {
	models := r.store.ModelsFor(entry.Name)
	if config.IsOpencodeEndpoint(entry.ApiURL) {
		for _, m := range models {
			if m == config.OpencodeDefaultModel {
				return m
			}
		}
	}
	for _, m := range models {
		if strings.TrimSpace(m) != "" {
			return m
		}
	}
	if config.IsOpencodeEndpoint(entry.ApiURL) {
		return config.OpencodeDefaultModel
	}
	return ""
}

func (r *AgentRuntime) ListProviders() []config.ProviderInfo {
	return r.store.ProviderInfos()
}

func (r *AgentRuntime) SetDefaultProvider(name string) ([]config.ProviderInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("provider name is required")
	}
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return nil, err
	}
	found := false
	for _, p := range file.Providers {
		if p.Name == name {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("provider %q not found", name)
	}
	file.DefaultProvider = name
	if err := r.store.SaveProviders(file); err != nil {
		return nil, err
	}
	return r.ListProviders(), nil
}

func (r *AgentRuntime) AddProvider(req config.ProviderInput) (config.ProviderInfo, error) {
	name, url, apiType, err := config.ValidateProviderInput(req, true)
	if err != nil {
		return config.ProviderInfo{}, err
	}
	key := strings.TrimSpace(req.ApiKey)
	return r.addProviderLocked(name, url, apiKeyString(apiType), key, req)
}

func apiKeyString(t config.ApiType) string { return string(t) }

func (r *AgentRuntime) addProviderLocked(name, url, apiType, key string, req config.ProviderInput) (config.ProviderInfo, error) {
	at, _ := config.ParseApiType(apiType)
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return config.ProviderInfo{}, err
	}
	for _, p := range file.Providers {
		if strings.EqualFold(p.Name, name) {
			return config.ProviderInfo{}, fmt.Errorf("provider %q already exists", name)
		}
	}
	file.Providers = append(file.Providers, config.Provider{
		Name: name, ApiURL: url, ApiType: at,
	})
	// Write the secret first: if the keychain is unavailable the catalog
	// is left untouched instead of gaining a provider without a key.
	if err := r.store.Keys.Set(name, key); err != nil {
		return config.ProviderInfo{}, err
	}
	if err := r.store.SaveProviders(file); err != nil {
		// The catalog write failed, so the provider does not exist: drop
		// the orphan secret rather than leaving it behind.
		_ = r.store.Keys.Delete(name)
		return config.ProviderInfo{}, err
	}
	r.evictProviderCache()
	return r.findProviderInfo(name)
}

func (r *AgentRuntime) findProviderInfo(name string) (config.ProviderInfo, error) {
	for _, p := range r.ListProviders() {
		if p.Name == name {
			return p, nil
		}
	}
	return config.ProviderInfo{}, fmt.Errorf("provider %q not found", name)
}

func (r *AgentRuntime) UpdateProvider(req config.ProviderInput) (config.ProviderInfo, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return config.ProviderInfo{}, fmt.Errorf("provider name is required")
	}
	validName, url, apiType, err := config.ValidateProviderInput(req, false)
	if err != nil {
		return config.ProviderInfo{}, err
	}
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return config.ProviderInfo{}, err
	}
	found := false
	for i, p := range file.Providers {
		if p.Name == name {
			file.Providers[i].ApiURL = url
			file.Providers[i].ApiType = apiType
			found = true
		}
	}
	if !found {
		return config.ProviderInfo{}, fmt.Errorf("provider %q not found", name)
	}
	// An empty key keeps the stored one.
	if strings.TrimSpace(req.ApiKey) != "" {
		if err := r.store.Keys.Set(name, strings.TrimSpace(req.ApiKey)); err != nil {
			return config.ProviderInfo{}, err
		}
	}
	if err := r.store.SaveProviders(file); err != nil {
		return config.ProviderInfo{}, err
	}
	r.evictProviderCache()
	return r.findProviderInfo(validName)
}
