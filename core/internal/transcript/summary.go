package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Ady-JK/paladin/core/internal/syserr"
)

// summary caches what the session list shows about a transcript, so a
// list does not re-read every conversation. Scanned is how many bytes
// of complete lines it covers: when the file grows by appends only the
// new tail is read, and Truncate (the only rewrite) drops the cache.
type summary struct {
	Scanned int64  `json:"scanned"`
	Count   int    `json:"count"`
	Title   string `json:"title"`
}

const summaryFile = "summary.json"

// onScan is set by tests to observe transcript reads.
var onScan func(dir string, from int64)

// sessionSummary returns the transcript's visible message count and
// first title, from the cache when it is current.
func sessionSummary(dir string) (summary, error) {
	st, err := os.Stat(filepath.Join(dir, "messages.jsonl"))
	if os.IsNotExist(err) {
		return summary{}, nil
	}
	if err != nil {
		return summary{}, fmt.Errorf("io: stat transcript: %s", syserr.Str(err))
	}
	cached, ok := readSummary(dir)
	if ok && cached.Scanned == st.Size() {
		return cached, nil
	}
	base := summary{}
	if ok && cached.Scanned < st.Size() && lineStartsAt(dir, cached.Scanned) {
		base = cached
	}
	next := base
	end, err := scanTranscriptFrom(dir, base.Scanned, false, func(m Message) {
		if !m.Visible() {
			return
		}
		next.Count++
		if next.Title == "" {
			next.Title = TitleFor([]Message{m})
		}
	})
	if err != nil {
		return summary{}, err
	}
	next.Scanned = end
	if !ok || next != cached {
		writeSummary(dir, next)
	}
	return next, nil
}

func readSummary(dir string) (summary, bool) {
	data, err := os.ReadFile(filepath.Join(dir, summaryFile))
	if err != nil {
		return summary{}, false
	}
	var s summary
	if json.Unmarshal(data, &s) != nil || s.Scanned < 0 || s.Count < 0 {
		return summary{}, false
	}
	return s, true
}

// writeSummary is best effort: a missing cache only costs a rescan.
func writeSummary(dir string, s summary) {
	data, err := json.Marshal(s)
	if err == nil {
		_ = atomicWrite(dir, summaryFile, data)
	}
}

func dropSummary(dir string) error {
	if err := os.Remove(filepath.Join(dir, summaryFile)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("io: drop summary: %s", syserr.Str(err))
	}
	return nil
}

// lineStartsAt reports whether offset is the start of a line.
func lineStartsAt(dir string, offset int64) bool {
	if offset == 0 {
		return true
	}
	f, err := os.Open(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 1)
	_, err = f.ReadAt(b, offset-1)
	return err == nil && b[0] == '\n'
}

// scanTranscriptFrom streams messages.jsonl from offset (a line start),
// calling fn per message, so a long transcript never sits in memory as
// raw bytes. It returns the offset just past the last complete line. A
// complete line that does not decode is an error. An unterminated last
// line is skipped when it does not decode (a torn append: the process
// died mid-write); one that does is passed to fn only with withTail,
// since the next append cuts it off.
func scanTranscriptFrom(dir string, offset int64, withTail bool, fn func(Message)) (int64, error) {
	if onScan != nil {
		onScan(dir, offset)
	}
	f, err := os.Open(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("io: open transcript: %s", syserr.Str(err))
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("io: seek transcript: %s", syserr.Str(err))
	}
	end := offset
	br := bufio.NewReaderSize(f, 64*1024)
	for {
		line, rerr := br.ReadBytes('\n')
		if rerr != nil && rerr != io.EOF {
			return 0, fmt.Errorf("io: read transcript: %s", syserr.Str(rerr))
		}
		if rerr == io.EOF {
			if trimmed := bytes.TrimSpace(line); withTail && len(trimmed) > 0 {
				if msgs, err := decodeLine(trimmed); err == nil {
					for _, msg := range msgs {
						fn(msg)
					}
				}
			}
			return end, nil
		}
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			msgs, err := decodeLine(trimmed)
			if err != nil {
				return 0, err
			}
			for _, msg := range msgs {
				fn(msg)
			}
		}
		end += int64(len(line))
	}
}
