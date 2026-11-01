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

// spillTo makes the capture keep every byte at path once the output
// outgrows its in-memory cap.
func (c *shellOutputCapture) spillTo(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spillPath = path
}

// spillLocked tees p to the spill file, opening it on the first write
// that crosses the cap. Until then head and tail still hold every byte,
// so they seed the file in order. A failure just drops the spill.
func (c *shellOutputCapture) spillLocked(p []byte) {
	if c.spillPath == "" || c.spillFailed {
		return
	}
	if c.spill == nil {
		if c.total+int64(len(p)) <= int64(c.maxBytes) {
			return
		}
		if err := os.MkdirAll(filepath.Dir(c.spillPath), 0o700); err != nil {
			c.spillFailed = true
			return
		}
		f, err := os.OpenFile(c.spillPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			c.spillFailed = true
			return
		}
		c.spill = f
		c.teeLocked(c.head)
		c.teeLocked(c.tail)
	}
	c.teeLocked(p)
}

func (c *shellOutputCapture) teeLocked(p []byte) {
	if c.spillFailed || c.spilled >= maxSpillBytes {
		return
	}
	p = p[:min(len(p), int(maxSpillBytes-c.spilled))]
	n, err := c.spill.Write(p)
	c.spilled += int64(n)
	if err != nil {
		c.spillFailed = true
	}
}

// finishSpill closes the spill and rewrites it the way the inline
// output reads (no ANSI, LF endings, redacted). It returns the path, or
// "" when nothing was spilled or the file could not be kept.
func (c *shellOutputCapture) finishSpill() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spill == nil {
		return ""
	}
	closeErr := c.spill.Close()
	c.spill = nil
	if c.spillFailed || closeErr != nil {
		_ = os.Remove(c.spillPath)
		return ""
	}
	raw, err := os.ReadFile(c.spillPath)
	if err != nil {
		return ""
	}
	text := ToLF(StripANSI(strings.ToValidUTF8(string(raw), "�")))
	if c.spilled >= maxSpillBytes {
		text += spillCutNote
	}
	if writeSpill(c.spillPath, text) != nil {
		_ = os.Remove(c.spillPath)
		return ""
	}
	return c.spillPath
}
