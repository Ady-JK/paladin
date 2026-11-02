// Package errlog appends raw errors to a session's error.log.
//
// Every session folder gets its own error.log, and so does every
// subagent session (a child is a session with its own folder). Lines are
// the unedited error text: no redaction, no truncation, no dedup — the
// file exists so a bug can be understood later from what actually
// happened. Timestamps are UTC so logs from several machines line up.
//
// Writing is best-effort: a full disk or a missing folder must never
// change how a turn behaves. The one bound kept is size: a file that
// outgrows maxBytes rotates to error.log.1, so a runaway loop cannot
// fill the disk. Nothing is dropped silently before that.
package errlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileName is the per-session log every error lands in.
const FileName = "error.log"

// maxBytes is when error.log rotates to error.log.1 (16 MiB).
const maxBytes = 16 << 20

// mu serializes appends: parallel tool batches and child turns can log
// at the same moment.
var mu sync.Mutex

// Append writes err to <dir>/error.log under source. A nil error or an
// empty dir is a no-op: sessions always have a folder, and a caller
// without one has nothing useful to write.
func Append(dir, source string, err error) {
	if err == nil {
		return
	}
	AppendText(dir, source, err.Error())
}

// Appendf writes a formatted line to <dir>/error.log.
func Appendf(dir, source, format string, args ...any) {
	AppendText(dir, source, fmt.Sprintf(format, args...))
}

// AppendText writes one entry to <dir>/error.log. The text is kept
// verbatim: embedded newlines (stack traces, multi-line provider
// bodies) stay as they are.
func AppendText(dir, source, text string) {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(text) == "" {
		return
	}
	write(filepath.Join(dir, FileName), source, text)
}

// Global writes an entry for work that has no session yet (a bad
// request, a failed open, a host panic) to <dataDir>/logs/errors.log.
func Global(dataDir, source, text string) {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(text) == "" {
		return
	}
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	write(filepath.Join(dir, "errors.log"), source, text)
}

// write appends one timestamped line, rotating first when the file has
// grown past maxBytes.
func write(path, source, text string) {
	mu.Lock()
	defer mu.Unlock()
	rotate(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s\t%s\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), source, text)
}

func rotate(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() < maxBytes {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
}
