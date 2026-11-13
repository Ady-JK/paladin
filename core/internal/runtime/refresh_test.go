package runtime

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/config"
)

func TestLiteLLMRefreshUpdatesVisibleModels(t *testing.T) {
	rt, _ := testRuntime(t)
	f := &recordingFetcher{models: map[string][]string{config.LiteLLMName: {"old", "new"}}}
	rt.SetModelFetcher(f.fetch)
	if _, err := rt.RefreshProvider(config.LiteLLMName); err != nil {
		t.Fatal(err)
	}
	f.models[config.LiteLLMName] = []string{"new", "latest"}
	result, err := rt.RefreshProvider(config.LiteLLMName)
	if err != nil || result.Error != "" || result.Count != 2 {
		t.Fatalf("refresh failed: %+v, %v", result, err)
	}
	want := []string{"latest", "new"}
	if got := rt.store.ModelsFor(config.LiteLLMName); !reflect.DeepEqual(got, want) {
		t.Fatalf("visible LiteLLM models = %v, want %v", got, want)
	}
	for _, provider := range rt.ListProviders() {
		if provider.Name == config.LiteLLMName && !reflect.DeepEqual(provider.Models, want) {
			t.Fatalf("picker models = %v, want %v", provider.Models, want)
		}
	}
	if got := f.calls(); !reflect.DeepEqual(got, []string{config.LiteLLMName, config.LiteLLMName}) {
		t.Fatalf("unexpected providers contacted: %v", got)
	}
	f.errs = map[string]error{config.LiteLLMName: errors.New("proxy unavailable")}
	failed, err := rt.RefreshProvider(config.LiteLLMName)
	if err != nil || failed.Error == "" {
		t.Fatalf("expected proxy error, got %+v, %v", failed, err)
	}
	if got := rt.store.ModelsFor(config.LiteLLMName); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed refresh changed catalog: %v", got)
	}
	delete(f.errs, config.LiteLLMName)
	f.models[config.LiteLLMName] = []string{}
	if _, err := rt.RefreshProvider(config.LiteLLMName); err != nil {
		t.Fatal(err)
	}
	if got := rt.store.ModelsFor(config.LiteLLMName); len(got) != 0 {
		t.Fatalf("empty proxy catalog retained old models: %v", got)
	}
}

// recordingFetcher stands in for the network so tests prove which
// providers would have been contacted without contacting any.
type recordingFetcher struct {
	mu     sync.Mutex
	called []string
	models map[string][]string
	errs   map[string]error
}

func (f *recordingFetcher) fetch(_ context.Context, p config.Provider, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = append(f.called, p.Name)
	if err := f.errs[p.Name]; err != nil {
		return nil, err
	}
	return f.models[p.Name], nil
}

func (f *recordingFetcher) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.called...)
}

func TestAddAndUpdateProviderNeverFetchModels(t *testing.T) {
	rt, _ := testRuntime(t)
	f := &recordingFetcher{}
	rt.SetModelFetcher(f.fetch)
	if _, err := rt.AddProvider(config.ProviderInput{Name: "local", ApiURL: "http://127.0.0.1:1/v1", ApiKey: "sk-x", ApiType: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.UpdateProvider(config.ProviderInput{Name: "local", ApiURL: "http://127.0.0.1:2/v1", ApiType: "openai"}); err != nil {
		t.Fatal(err)
	}
	rt.WaitRefresh()
	if got := f.calls(); len(got) != 0 {
		t.Fatalf("add/update must not contact any provider, contacted %v", got)
	}
}

func TestRefreshProviderOnlyContactsNamedProvider(t *testing.T) {
	rt, _ := testRuntime(t)
	f := &recordingFetcher{models: map[string][]string{"local": {"zeta", "alpha"}}}
	rt.SetModelFetcher(f.fetch)
	if _, err := rt.AddProvider(config.ProviderInput{Name: "local", ApiURL: "http://127.0.0.1:1/v1", ApiKey: "sk-x", ApiType: "openai"}); err != nil {
		t.Fatal(err)
	}
	res, err := rt.RefreshProvider("local")
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "local" || res.Count != 2 || res.Error != "" {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := f.calls(); len(got) != 1 || got[0] != "local" {
		t.Fatalf("only the named provider may be contacted, got %v", got)
	}
	models, _ := rt.store.LoadModels()
	if all := models.Providers["local"].AllModels; len(all) != 2 || all[0] != "alpha" {
		t.Fatalf("catalog not saved sorted: %+v", all)
	}
}

func TestRefreshProviderReportsFetchError(t *testing.T) {
	rt, _ := testRuntime(t)
	f := &recordingFetcher{errs: map[string]error{"openrouter": errors.New("status 401")}}
	rt.SetModelFetcher(f.fetch)
	res, err := rt.RefreshProvider("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || res.Count != 0 {
		t.Fatalf("fetch error must surface in the result: %+v", res)
	}
}

func TestRefreshProviderUnknownName(t *testing.T) {
	rt, _ := testRuntime(t)
	rt.SetModelFetcher((&recordingFetcher{}).fetch)
	if _, err := rt.RefreshProvider("nope"); err == nil {
		t.Fatal("unknown provider must error")
	}
}

func TestRefreshAllUsesInjectedFetcher(t *testing.T) {
	rt, _ := testRuntime(t)
	f := &recordingFetcher{}
	rt.SetModelFetcher(f.fetch)
	if !rt.RefreshProviders() {
		t.Fatal("refresh should start")
	}
	rt.WaitRefresh()
	if len(f.calls()) == 0 {
		t.Fatal("manual refresh-all should go through the injected fetcher")
	}
}
