package session

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// ledgerMu serialises usage.json read-modify-writes: a turn and a side
// question can bill the same session at once.
var ledgerMu sync.Mutex

// RecordCall folds one LLM call into the usage.json at path. It is
// best-effort: billing must never fail a turn, so errors are swallowed
// and a corrupt ledger restarts fresh.
func RecordCall(path, sessionID, model string, call Counters) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	var totals UsageTotals
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &totals) != nil {
			totals = UsageTotals{}
		}
	}
	totals.SessionID = sessionID
	totals.AddCall(model, call, time.Now().UTC().Unix())
	if data, err := json.MarshalIndent(totals, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
}
