package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecordCallIsSafeAcrossConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RecordCall(path, "s1", "m", Counters{PromptTokens: 1, Cost: 0.5, HasCost: true})
		}()
	}
	wg.Wait()
	var totals UsageTotals
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &totals); err != nil {
		t.Fatal(err)
	}
	if totals.Calls != 40 || totals.PromptTokens != 40 || totals.Cost != 20 || totals.SessionID != "s1" || totals.Model != "m" {
		t.Fatalf("lost updates: %+v", totals.Counters)
	}
}

func TestRecordCallPreservesACorruptLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	if err := os.WriteFile(path, []byte(`{"cost":7.5,"hasCost":true,"calls":`), 0o644); err != nil {
		t.Fatal(err)
	}
	RecordCall(path, "s1", "m", Counters{TotalTokens: 3, Cost: 0.25, HasCost: true})
	totals, err := ReadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if totals.Calls != 1 || totals.Cost != 0.25 || totals.RecoveredFrom == "" {
		t.Fatalf("new ledger must count on and point at the old one: %+v", totals)
	}
	if !totals.SpendIncomplete() {
		t.Fatal("spend after a corrupt ledger is a lower bound")
	}
	old, err := os.ReadFile(filepath.Join(dir, totals.RecoveredFrom))
	if err != nil || string(old) != `{"cost":7.5,"hasCost":true,"calls":` {
		t.Fatalf("corrupt ledger not preserved: %q %v", old, err)
	}
	RecordCall(path, "s1", "m", Counters{TotalTokens: 1})
	if again, _ := ReadLedger(path); again.RecoveredFrom != totals.RecoveredFrom || again.Calls != 2 {
		t.Fatalf("recovery marker must stick: %+v", again)
	}
}

func TestReadLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	if got, err := ReadLedger(path); err != nil || got.Calls != 0 {
		t.Fatalf("missing ledger: %+v %v", got, err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLedger(path); !errors.Is(err, ErrLedgerCorrupt) {
		t.Fatalf("truncated ledger: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLedger(path); !errors.Is(err, ErrLedgerCorrupt) {
		t.Fatalf("empty ledger: %v", err)
	}
}

func TestRecordCallWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	RecordCall(path, "s1", "m", Counters{TotalTokens: 1})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode: %v %v", st.Mode(), err)
	}
}
