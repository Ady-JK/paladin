// spill.go keeps oversized tool output whole. Instead of losing the
// middle to truncation, the full text is saved under the session's
// tool-output folder (removed with the session) and the model gets the
// head, the tail and the file's path to read or grep the rest.

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/redact"
)

const (
	// ToolOutputDir is the session subfolder holding spilled output.
	ToolOutputDir = "tool-output"
	// maxSpillBytes bounds one spilled result on disk.
	maxSpillBytes = 8 << 20
	spillCutNote  = "\n… [cut at 8 MB]\n"
)

// SpillPath is where the running call's full output is kept, or "" when
// there is no session to keep it in.
func SpillPath(ctx *ToolCtx) string {
	if ctx == nil || ctx.SessionDir == "" {
		return ""
	}
	id := safeFileName(ctx.CallID)
	if id == "" {
		id = fmt.Sprintf("out-%d", time.Now().UnixNano())
	}
	return filepath.Join(ctx.SessionDir, ToolOutputDir, id+".txt")
}

// SpillText saves full, redacted, for the running call and returns its
// path, or "" when it could not be kept.
func SpillText(ctx *ToolCtx, full string) string {
	path := SpillPath(ctx)
	if path == "" || writeSpill(path, full) != nil {
		return ""
	}
	return path
}

// SpillNote tells the model where the omitted part is.
func SpillNote(path string) string {
	return "[full output saved to " + path + "; read or grep it for the omitted part]"
}

func writeSpill(path, text string) error {
	text = redact.String(text)
	if len(text) > maxSpillBytes+len(spillCutNote) {
		cut := maxSpillBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + spillCutNote
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

// safeFileName keeps the characters of a call id that are safe in a
// file name on every OS.
func safeFileName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// spillTo, spillLocked, teeLocked and finishSpill are intentionally
// gone: shell commands write straight to a job log, so there is no
// in-memory capture to spill.
