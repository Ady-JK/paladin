// Package runtime: refresh.go fetches provider model catalogs. Fetching
// is always user-initiated: nothing here runs on startup or when a
// provider is added or edited, so no provider is contacted unless the
// user asks for it.
package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
)

// refreshTimeout bounds one provider's listing so a dead host cannot
// pin the refresh.
const refreshTimeout = 30 * time.Second

// ModelFetcher lists the model ids one provider serves. Injected so
// tests never touch the network.
type ModelFetcher func(ctx context.Context, p config.Provider, apiKey string) ([]string, error)

// RefreshResult is the outcome of refreshing one provider's catalog.
type RefreshResult struct {
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

func networkModelFetcher(ctx context.Context, p config.Provider, apiKey string) ([]string, error) {
	if config.IsPlaceholderKey(apiKey) {
		apiKey = "public-listing"
	}
	provider, err := inference.BuildProvider(p.ApiType, p.Name, p.Model, p.ApiURL, apiKey)
	if err != nil {
		return nil, err
	}
	return provider.ListModels(ctx)
}

// SetModelFetcher replaces how catalogs are fetched (tests, offline mode).
func (r *AgentRuntime) SetModelFetcher(f ModelFetcher) {
	if f == nil {
		f = networkModelFetcher
	}
	r.fetchMu.Lock()
	defer r.fetchMu.Unlock()
	r.fetchModels = f
}

func (r *AgentRuntime) fetcher() ModelFetcher {
	r.fetchMu.RLock()
	defer r.fetchMu.RUnlock()
	return r.fetchModels
}

// WaitRefresh blocks until a detached refresh-all finishes.
func (r *AgentRuntime) WaitRefresh() { r.refreshWG.Wait() }

// RefreshProviders refreshes every provider's catalog in the
// background. Only the explicit "refresh all" action calls it; the
// false return means a refresh is already running.
func (r *AgentRuntime) RefreshProviders() bool {
	if !r.refreshing.CompareAndSwap(false, true) {
		return false
	}
	fetch := r.fetcher()
	r.refreshWG.Add(1)
	go func() {
		defer r.refreshWG.Done()
		defer r.refreshing.Store(false)
		store := config.NewStore(r.dataDir, r.store.Keys)
		_ = refreshCatalog(store, nil, fetch)
	}()
	return true
}

// RefreshProvider synchronously refreshes one provider's catalog and
// contacts no other provider. A fetch failure is reported in the
// result (the stored catalog is kept); an unknown name is an error.
func (r *AgentRuntime) RefreshProvider(name string) (RefreshResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return RefreshResult{}, fmt.Errorf("provider name is required")
	}
	file, err := r.store.LoadProviders()
	if err != nil {
		return RefreshResult{}, err
	}
	found := false
	for _, p := range file.Providers {
		if p.Name == name {
			found = true
			break
		}
	}
	if !found {
		return RefreshResult{}, fmt.Errorf("provider %q not found", name)
	}
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	results, err := refreshCatalogResults(r.store, []string{name}, r.fetcher())
	if err != nil {
		return RefreshResult{}, err
	}
	return results[0], nil
}

func refreshCatalog(store *config.Store, names []string, fetch ModelFetcher) error {
	_, err := refreshCatalogResults(store, names, fetch)
	return err
}

// refreshCatalogResults fetches the named providers (all when names is
// empty) and saves every successful listing. Failures keep the old
// catalog entry and are reported per provider.
func refreshCatalogResults(store *config.Store, names []string, fetch ModelFetcher) ([]RefreshResult, error) {
	file, err := store.LoadProviders()
	if err != nil {
		return nil, err
	}
	catalog, err := store.LoadModels()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var results []RefreshResult
	for _, p := range file.Providers {
		if len(want) > 0 && !want[p.Name] {
			continue
		}
		res := RefreshResult{Name: p.Name}
		key, _, err := store.Keys.Get(p.Name)
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		models, err := fetch(ctx, p, key)
		cancel()
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		sort.Strings(models)
		entry := catalog.Providers[p.Name]
		entry.AllModels = models
		if len(entry.EnabledModels) == 0 {
			entry.EnabledModels = append([]string(nil), models...)
		}
		if catalog.Providers == nil {
			catalog.Providers = map[string]config.ProviderModels{}
		}
		catalog.Providers[p.Name] = entry
		res.Count, res.Models = len(models), models
		results = append(results, res)
	}
	if err := store.SaveModels(catalog); err != nil {
		return results, err
	}
	return results, nil
}
