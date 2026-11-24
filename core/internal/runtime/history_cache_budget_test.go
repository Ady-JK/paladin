package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// The cache holds a bounded number of transcript bytes, not just a bounded
// number of transcripts.
func TestHistoryCacheStaysWithinItsByteBudget(t *testing.T) {
	oldBudget, oldMax := historyCacheBudget, historyEntryMax
	historyCacheBudget, historyEntryMax = 6<<10, 4<<10
	t.Cleanup(func() { historyCacheBudget, historyEntryMax = oldBudget, oldMax })
	store := transcript.NewStore(t.TempDir())
	var c historyCache
	for i := 0; i < 5; i++ {
		sess, err := store.Create("/p", "prov", "m")
		if err != nil {
			t.Fatal(err)
		}
		if err := sess.AppendTurn([]transcript.Message{transcript.UserMessage(strings.Repeat("x", 2500)), {Role: transcript.RoleAssistant, Content: "ok"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.read(sess); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(sess.Dir, "messages.jsonl")); err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		size := c.size()
		c.mu.Unlock()
		if size > historyCacheBudget {
			t.Fatalf("after %d sessions the cache stands for %d bytes (budget %d)", i+1, size, historyCacheBudget)
		}
	}
}
