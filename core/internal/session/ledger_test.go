package session

import (
	"encoding/json"
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

func TestRecordCallRestartsACorruptLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	RecordCall(path, "s1", "m", Counters{TotalTokens: 3})
	data, _ := os.ReadFile(path)
	var totals UsageTotals
	if err := json.Unmarshal(data, &totals); err != nil || totals.Calls != 1 || totals.TotalTokens != 3 {
		t.Fatalf("%s %v", data, err)
	}
}
