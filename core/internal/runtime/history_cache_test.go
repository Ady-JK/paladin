package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/session"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func BenchmarkHistoryPageFile(b *testing.B) {
	dir := b.TempDir()
	line := `{"role":"user","content":"` + strings.Repeat("x", 4096) + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(strings.Repeat(line, 2000)), 0600); err != nil {
		b.Fatal(err)
	}
	sess := transcript.Session{Dir: dir}
	var cache historyCache
	for _, tc := range []struct {
		name string
		read func() ([]transcript.Turn, error)
	}{
		{"uncached", sess.HistoryTurns},
		{"cached", func() ([]transcript.Turn, error) { return cache.read(sess) }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if _, err := tc.read(); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				turns, err := tc.read()
				if err != nil {
					b.Fatal(err)
				}
				msgs, stats := transcript.Flatten(turns)
				_ = session.HistoryPageStats(msgs, stats, 120, 500)
			}
		})
	}
}
