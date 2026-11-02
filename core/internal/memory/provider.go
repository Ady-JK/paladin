// provider.go is the memory backend seam.
//
// Today there is exactly one backend — the §-delimited files under
// <data>/memory — and that is deliberate: a second store without a clear
// need would split the truth in two. The seam exists so the rest of the
// system (the tool, the prompt path, the settings pane, the review loop)
// talks to an interface instead of to files, which is what makes a second
// backend a new type rather than a rewrite.
//
// A Provider is per-scope: it answers for one Target resolved against one
// workspace folder. Everything that must be shared between backends —
// tolerant matching, masking, the threat scan, budgets — stays in this
// package and is applied by the callers, so a backend only ever has to
// store and return entry lists.
//
// Adding a backend means: implement Provider, teach ProviderFor about its
// name, and extend validate() in config.go. Nothing else changes.

package memory

import (
	"fmt"
	"sort"
	"strings"
)

// Provider is one storage backend for one memory scope.
//
// The contract is intentionally narrow — read entries, fingerprint them —
// so a backend cannot invent a second set of write rules. Every mutation
// goes through batchProvider and the package-level Apply/ReplaceAll, which
// apply the guards, masking, scan, budget and tolerant matching exactly
// once.
type Provider interface {
	// Name is the provider's identifier, matching memory.json's provider.
	Name() string
	// Target is the scope this provider was resolved for.
	Target() Target
	// Path is a human-readable location for the settings pane.
	Path() string
	// Entries returns the stored entries, deduplicated, in stored order.
	Entries() ([]string, error)
	// Fingerprint identifies the stored content, so a session can tell
	// that another writer moved it since its snapshot.
	Fingerprint() string
}

// batchProvider upgrades a Provider to the full mutation surface the tool
// uses. Every rule lives here, once, so all backends behave identically:
// the write guards, secret masking, the threat scan, the budget, the
// tolerant matching, and the exclusive critical section.
type batchProvider interface {
	Provider
	// Mutate runs change against the freshest stored entries with the
	// scope's write lock held, then stores the result atomically. change
	// must not be called by the backend outside that critical section: a
	// read taken before the lock could be stale, and writing it back would
	// silently drop another writer's update.
	Mutate(change func(entries []string) ([]string, error)) (Result, error)
	// Limit is the configured character budget for the scope.
	Limit() int
}

// fileBatch is the built-in provider's full mutation surface: the file
// store's mutate already takes both locks, re-reads under them, checks the
// budget and swaps the file atomically.
type fileBatch struct {
	store  *Store
	target Target
}

func (f fileBatch) Name() string               { return ProviderFile }
func (f fileBatch) Target() Target             { return f.target }
func (f fileBatch) Path() string               { return f.store.Path() }
func (f fileBatch) Limit() int                 { return f.store.Limit() }
func (f fileBatch) Fingerprint() string        { return f.store.Hash() }
func (f fileBatch) Entries() ([]string, error) { return f.store.Entries() }
func (f fileBatch) Mutate(change func([]string) ([]string, error)) (Result, error) {
	return f.store.mutate(change, nil)
}

// Apply runs a batch against the provider: the same guards, scan, masking
// and budget as the file store's own Apply, but written once so a second
// backend cannot drift from them.
func Apply(p Provider, ops []Op) (Result, error) {
	bp, ok := p.(batchProvider)
	if !ok {
		return Result{}, fmt.Errorf("memory: provider %q cannot be written to", p.Name())
	}
	if len(ops) == 0 {
		return Result{}, fmt.Errorf("no operations")
	}
	masked := map[int]bool{}
	res, err := bp.Mutate(func(entries []string) ([]string, error) {
		for i, op := range ops {
			next, hit, err := applyOp(entries, op)
			if err != nil {
				return nil, fmt.Errorf("operation %d (%s): %w", i+1, op.Action, err)
			}
			entries = next
			if hit >= 0 {
				masked[hit] = true
			}
		}
		entries = dedupe(entries)
		// The file backend checks this inside its own critical section;
		// repeating it here keeps the guarantee for any backend.
		if n := used(entries); n > bp.Limit() {
			return nil, fmt.Errorf("memory limit exceeded: %d/%d chars — consolidate or remove entries in the same batch", n, bp.Limit())
		}
		return entries, nil
	})
	if err != nil {
		return Result{}, err
	}
	for i := range res.Entries {
		if masked[i] {
			res.Masked = append(res.Masked, i)
		}
	}
	return res, nil
}

// ReplaceAll stores entries as the scope's complete contents (the settings
// editor), applying the same per-entry rules as the batch path.
func ReplaceAll(p Provider, entries []string) (Result, error) {
	bp, ok := p.(batchProvider)
	if !ok {
		return Result{}, fmt.Errorf("memory: provider %q cannot be written to", p.Name())
	}
	masked := map[int]bool{}
	res, err := bp.Mutate(func([]string) ([]string, error) {
		out := []string{}
		for _, e := range entries {
			// Blank rows are dropped before validation so the editor can
			// carry a trailing empty line without complaining.
			if normalize(e) == "" {
				continue
			}
			clean, wasMasked, err := cleanEntry(e)
			if err != nil {
				return nil, err
			}
			if wasMasked {
				masked[len(out)] = true
			}
			out = append(out, clean)
		}
		if n := used(out); n > bp.Limit() {
			return nil, fmt.Errorf("memory limit exceeded: %d/%d chars — consolidate or remove entries", n, bp.Limit())
		}
		return out, nil
	})
	if err != nil {
		return Result{}, err
	}
	for i := range res.Entries {
		if masked[i] {
			res.Masked = append(res.Masked, i)
		}
	}
	return res, nil
}

// knownProviders are the backend names memory.json may select.
func knownProviders() []string {
	names := []string{ProviderFile}
	sort.Strings(names)
	return names
}

// providerName is the configured backend for a bank.
func (b *Bank) providerName() string {
	if name := strings.TrimSpace(b.cfg.Provider); name != "" {
		return name
	}
	return ProviderFile
}

// ProviderFor resolves the configured backend for one scope. Only the file
// backend exists, so an unknown name is a configuration error rather than
// a silent fallback: memory.json is validated before it is saved, and a
// hand-edited unknown value should say so.
func (b *Bank) ProviderFor(target Target, cwd string) (Provider, error) {
	name := b.providerName()
	if name != ProviderFile {
		return nil, fmt.Errorf("memory: provider %q is not available (built in: %s)",
			name, strings.Join(knownProviders(), ", "))
	}
	s, err := b.Store(target, cwd)
	if err != nil {
		return nil, err
	}
	return &fileProvider{fileBatch{store: s, target: target}}, nil
}

// fileProvider is the file backend, presented as a Provider.
type fileProvider struct{ fileBatch }
