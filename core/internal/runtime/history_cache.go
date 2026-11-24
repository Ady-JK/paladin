package runtime

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// historyCacheBudget bounds the transcript bytes the cache stands for (decoded,
// they take several times that in memory); historyEntryMax keeps one huge
// transcript from using it all. Variables so tests can shrink them.
var (
	historyCacheBudget int64 = 48 << 20
	historyEntryMax    int64 = 16 << 20
)

// A small, bounded cache avoids decoding the same transcript on every page.
// The file identity, size and modification time guard appends and rewrites.
type historyCache struct {
	mu      sync.Mutex
	entries map[string]cachedHistory
}

type cachedHistory struct {
	file  os.FileInfo
	turns []transcript.Turn
	used  time.Time
}

func sameHistoryFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (c *historyCache) read(sess transcript.Session) ([]transcript.Turn, error) {
	path := filepath.Join(sess.Dir, "messages.jsonl")
	before, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sess.HistoryTurns()
		}
		return nil, err
	}
	c.mu.Lock()
	if entry, ok := c.entries[path]; ok && sameHistoryFile(entry.file, before) {
		entry.used = time.Now()
		c.entries[path] = entry
		c.mu.Unlock()
		return entry.turns, nil
	}
	c.mu.Unlock()
	turns, err := sess.HistoryTurns()
	if err != nil {
		return nil, err
	}
	after, err := os.Stat(path)
	if err != nil || !sameHistoryFile(before, after) || after.Size() > historyEntryMax {
		return turns, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]cachedHistory)
	}
	delete(c.entries, path)
	for len(c.entries) >= 8 || (len(c.entries) > 0 && c.size()+after.Size() > historyCacheBudget) {
		var oldest string
		var at time.Time
		for key, entry := range c.entries {
			if oldest == "" || entry.used.Before(at) {
				oldest, at = key, entry.used
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[path] = cachedHistory{file: after, turns: turns, used: time.Now()}
	return turns, nil
}

// size is the transcript bytes the cached entries stand for.
func (c *historyCache) size() int64 {
	var total int64
	for _, e := range c.entries {
		total += e.file.Size()
	}
	return total
}
