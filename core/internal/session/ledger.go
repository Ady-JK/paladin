package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

// ledgerMu serialises usage.json read-modify-writes: a turn and a side
// question can bill the same session at once.
var ledgerMu sync.Mutex

// ErrLedgerCorrupt means usage.json exists but does not decode.
var ErrLedgerCorrupt = errors.New("usage ledger is corrupt")

// ReadLedger loads usage.json. A missing file is zero totals; one that
// does not decode (truncated, empty, garbage) is ErrLedgerCorrupt, never
// zero, so a broken ledger cannot pass for "nothing spent".
func ReadLedger(path string) (UsageTotals, error) {
	var totals UsageTotals
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return totals, nil
	}
	if err != nil {
		return totals, err
	}
	if err := json.Unmarshal(data, &totals); err != nil {
		return UsageTotals{}, fmt.Errorf("%w: %w", ErrLedgerCorrupt, err)
	}
	return totals, nil
}

// RecordCall folds one LLM call into the usage.json at path. Billing
// must never fail a turn, so errors are logged, not returned. A corrupt
// ledger is renamed aside (usage.json.corrupt-<unix>) and a new one
// starts with RecoveredFrom naming it, so the lost spend stays visible.
// A ledger that cannot be read at all is left untouched.
func RecordCall(path, sessionID, model string, call Counters) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	totals, err := ReadLedger(path)
	if errors.Is(err, ErrLedgerCorrupt) {
		aside := fmt.Sprintf("%s.corrupt-%d", filepath.Base(path), time.Now().UnixNano())
		if rerr := os.Rename(path, filepath.Join(filepath.Dir(path), aside)); rerr != nil {
			log.Printf("usage: corrupt ledger %s could not be set aside: %v", path, rerr)
			return
		}
		log.Printf("usage: corrupt ledger moved to %s; spend restarts as a lower bound", aside)
		totals = UsageTotals{RecoveredFrom: aside}
	} else if err != nil {
		log.Printf("usage: ledger %s unreadable, call not recorded: %v", path, err)
		return
	}
	totals.SessionID = sessionID
	totals.AddCall(model, call, time.Now().UTC().Unix())
	data, err := json.MarshalIndent(totals, "", "  ")
	if err == nil {
		err = atomicfile.Write(path, data)
	}
	if err != nil {
		log.Printf("usage: write ledger %s: %v", path, err)
	}
}
