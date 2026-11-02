// refresh.go fetches provider model catalogs. Fetching
// is always user-initiated: nothing here runs on startup or when a
// provider is added or edited, so no provider is contacted unless the
// user asks for it.

package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/inference"
)

// refreshTimeout bounds one provider's listing so a dead host cannot
// pin the refresh.
const refreshTimeout = 30 * time.Second

// CatalogFetch is one provider's listing: the model ids plus whatever
// metadata the provider publishes about them (nil when it publishes
// none, which is every provider but OpenCode Go today).
type CatalogFetch struct {
	Models []string
	Meta   map[string]config.ModelMeta
}

// ModelFetcher lists the models one provider serves. Injected so
// tests never touch the network.
type ModelFetcher func(ctx context.Context, p config.Provider, apiKey string) ([]string, error)

// CatalogFetcher lists the models one provider serves along with their
// metadata. Injected so tests never touch the network.
type CatalogFetcher func(ctx context.Context, p config.Provider, apiKey string) (CatalogFetch, error)

// RefreshResult is the outcome of refreshing one provider's catalog.
type RefreshResult struct {
	Name   string   `json:"name"`
	Count  int      `json:"count"`
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

func networkModelFetcher(ctx context.Context, p config.Provider, apiKey string) (CatalogFetch, error) {
	// BuildProvider refuses a placeholder key, but the listing is public:
	// the catalog read only needs a provider to talk to. A placeholder is
	// not a real credential, so it must not be sent as one — the gateway
	// answers a bogus Bearer token with 401 while a header-less request
	// succeeds.
	publicListing := config.IsPlaceholderKey(apiKey)
	if publicListing {
		apiKey = "public-listing"
	}
	provider, err := inference.BuildProvider(p.ApiType, p.Name, p.Model, p.ApiURL, apiKey)
	if err != nil {
		return CatalogFetch{}, err
	}
	if publicListing {
		provider = inference.WithPublicListing(provider)
	}
	lister, ok := provider.(interface {
		ListCatalog(context.Context) ([]string, map[string]inference.CatalogMeta, error)
	})
	if !ok {
		models, err := provider.ListModels(ctx)
		return CatalogFetch{Models: models}, err
	}
	models, meta, err := lister.ListCatalog(ctx)
	if err != nil {
		return CatalogFetch{}, err
	}
	var out map[string]config.ModelMeta
	if len(meta) > 0 {
		out = make(map[string]config.ModelMeta, len(meta))
		for id, m := range meta {
			out[id] = m
		}
	}
	return CatalogFetch{Models: models, Meta: out}, nil
}

// SetModelFetcher replaces how catalogs are fetched (tests, offline mode).
// A fetcher that reports ids only keeps the metadata already stored.
func (r *AgentRuntime) SetModelFetcher(f ModelFetcher) {
	if f == nil {
		r.SetCatalogFetcher(nil)
		return
	}
	r.SetCatalogFetcher(func(ctx context.Context, p config.Provider, apiKey string) (CatalogFetch, error) {
		models, err := f(ctx, p, apiKey)
		return CatalogFetch{Models: models}, err
	})
}

// SetCatalogFetcher replaces how catalogs and their metadata are fetched
// (tests, offline mode).
func (r *AgentRuntime) SetCatalogFetcher(f CatalogFetcher) {
	if f == nil {
		f = networkModelFetcher
	}
	r.fetchMu.Lock()
	defer r.fetchMu.Unlock()
	r.fetchModels = f
}

func (r *AgentRuntime) fetcher() CatalogFetcher {
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
		results, err := refreshCatalogResults(store, nil, fetch)
		if err != nil {
			log.Printf("refresh: providers: %v", err)
		}
		for _, res := range results {
			if res.Error != "" {
				log.Printf("refresh: %s: %s", res.Name, res.Error)
			}
		}
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
	// A cached provider was built with the metadata known at the time, so
	// its wire dialect and thinking levels would otherwise stay stale for
	// the rest of the process: the catalog now says something new.
	r.evictProviderCache()
	return results[0], nil
}

// refreshCatalogResults fetches the named providers (all when names is
// empty) and saves every successful listing. Failures keep the old
// catalog entry and are reported per provider.
func refreshCatalogResults(store *config.Store, names []string, fetch CatalogFetcher) ([]RefreshResult, error) {
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
		fetched, err := fetch(ctx, p, key)
		cancel()
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		models := fetched.Models
		sort.Strings(models)
		entry := catalog.Providers[p.Name]
		entry.AllModels = models
		if len(entry.EnabledModels) == 0 {
			entry.EnabledModels = append([]string(nil), models...)
		}
		// A listing that carries no metadata says nothing about its
		// models, so whatever was learned before still stands.
		entry.Meta = mergeMeta(entry.Meta, fetched.Meta, models)
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

// mergeMeta folds a fresh listing's metadata into the stored map: models
// the listing described take its values, models it stayed silent about
// keep theirs, and models it no longer serves are dropped so a stale
// entry never outlives its model.
func mergeMeta(stored, fresh map[string]config.ModelMeta, models []string) map[string]config.ModelMeta {
	if len(stored) == 0 && len(fresh) == 0 {
		return nil
	}
	out := make(map[string]config.ModelMeta, len(models))
	for _, id := range models {
		if m, ok := fresh[id]; ok && !m.Empty() {
			out[id] = m
			continue
		}
		if m, ok := stored[id]; ok && !m.Empty() {
			out[id] = m
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
