// fileengine.go is the shared engine behind write/edit.

package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/syserr"
)

const MaxFileBytes = 10 << 20

// Engine is the shared write/edit engine. Its per-file locks and
// per-session version table are process-wide (see filestate.go).
type Engine struct{}

func NewEngine() Engine { return Engine{} }

type WriteResult struct {
	RequestedPath string `json:"requested_path"`
	ResolvedPath  string `json:"resolved_path"`
	Scope         string `json:"scope"`
	Created       bool   `json:"created"`
	Overwritten   bool   `json:"overwritten"`
	Bytes         int    `json:"bytes"`
	Lines         int    `json:"lines"`
}

type EditResult struct {
	RequestedPath  string      `json:"requested_path"`
	ResolvedPath   string      `json:"resolved_path"`
	Scope          string      `json:"scope"`
	Replacements   int         `json:"replacements"`
	Bytes          int         `json:"bytes"`
	Lines          int         `json:"lines"`
	MatchStartLine int         `json:"match_start_line"`
	MatchEndLine   int         `json:"match_end_line"`
	Matches        []MatchSpan `json:"matches"`
	// Strategy is how old_string matched (exact, line_endings,
	// whitespace, indentation); non-exact tells the model it drifted.
	Strategy string `json:"match_strategy"`
}

type MatchSpan struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type ContextLine struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type Occurrence struct {
	Occurrence int
	StartLine  int
	EndLine    int
	Before     *ContextLine
	After      *ContextLine
}

// EngineError is a file-engine failure with a model-facing message.
type EngineError struct {
	Kind          string
	Message       string
	RequestedPath string
	ResolvedPath  string
	Scope         string
	Count         int
	Occurrences   []Occurrence
	// Reason qualifies a "conflict": ConflictUnread, ConflictStale or
	// ConflictChanged.
	Reason string
}

func (e *EngineError) Error() string { return e.Message }

func conflict(tool, reason, requested, resolved, scope string) *EngineError {
	var why string
	switch reason {
	case ConflictUnread:
		why = "already exists and this session has not read all of its current content"
	case ConflictStale:
		why = "changed since this session last read or wrote it (another session, a shell command or an external program)"
	case ConflictTruncated:
		return &EngineError{
			Kind:   "conflict",
			Reason: reason,
			Message: fmt.Sprintf("%s: %s has lines longer than read shows (marked \"%s\"), so this session has not seen all of it and cannot replace it whole; nothing was written. "+
				"Change it with the edit tool instead", tool, resolved, strings.TrimSpace(truncatedMarker)),
			RequestedPath: requested,
			ResolvedPath:  resolved,
			Scope:         scope,
		}
	default:
		why = "changed while this call was preparing its write"
	}
	return &EngineError{
		Kind:          "conflict",
		Reason:        reason,
		Message:       fmt.Sprintf("%s: %s %s; nothing was written. Read the current file (every page), merge your changes, and retry", tool, resolved, why),
		RequestedPath: requested,
		ResolvedPath:  resolved,
		Scope:         scope,
	}
}

// Write creates or (with overwrite) replaces a file. Replacing requires
// that the session has read the whole current version (see notePage) or
// wrote it last; otherwise it returns a conflict and writes nothing.
func (eng Engine) Write(session, workspace, requestedPath, content string, overwrite bool) (WriteResult, error) {
	if strings.TrimSpace(requestedPath) == "" {
		return WriteResult{}, &EngineError{Kind: "message", Message: "write: path is required"}
	}
	resolved, scope, err := ResolveScopedPath("write", "path", requestedPath, workspace)
	if err != nil {
		return WriteResult{}, &EngineError{Kind: "message", Message: err.Error()}
	}
	unlock := lockPath(resolved)
	defer unlock()
	perm := os.FileMode(0o644)
	current := ""
	info, statErr := os.Stat(resolved)
	existed := statErr == nil
	if existed {
		if info.IsDir() {
			return WriteResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("write: path %q is a directory, not a file", requestedPath)}
		}
		if !overwrite {
			return WriteResult{}, &EngineError{
				Kind:          "exists",
				Message:       fmt.Sprintf("write: path %q already exists; pass overwrite to replace it", requestedPath),
				RequestedPath: requestedPath,
				ResolvedPath:  resolved,
				Scope:         scope,
			}
		}
		if p := info.Mode().Perm(); p != 0 {
			perm = p
		}
		if current, err = hashFile(resolved); err != nil {
			return WriteResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("write: path %q cannot be read: %s", requestedPath, syserr.Str(err))}
		}
		seen, ok := fileVersions.seenHash(session, resolved)
		if !ok {
			if h, clipped := fileVersions.clippedHash(session, resolved); clipped && h == current {
				return WriteResult{}, conflict("write", ConflictTruncated, requestedPath, resolved, scope)
			}
			return WriteResult{}, conflict("write", ConflictUnread, requestedPath, resolved, scope)
		}
		if seen != current {
			return WriteResult{}, conflict("write", ConflictStale, requestedPath, resolved, scope)
		}
	} else if !os.IsNotExist(statErr) {
		return WriteResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("write: path %q cannot be used: %s", requestedPath, syserr.Str(statErr))}
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return WriteResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("write: create parent directories: %s", syserr.Str(err))}
	}
	data := []byte(content)
	err = atomicReplace(resolved, data, perm, func() error {
		now, err := hashFile(resolved)
		if err != nil || now != current {
			return errChanged
		}
		return nil
	})
	if errors.Is(err, errChanged) {
		fileVersions.forget(session, resolved)
		return WriteResult{}, conflict("write", ConflictChanged, requestedPath, resolved, scope)
	}
	if err != nil {
		return WriteResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("write: %s; the file was not changed", syserr.Str(err))}
	}
	fileVersions.record(session, resolved, hashBytes(data))
	return WriteResult{
		RequestedPath: requestedPath,
		ResolvedPath:  resolved,
		Scope:         scope,
		Created:       !existed,
		Overwritten:   existed,
		Bytes:         len(content),
		Lines:         CountLines(content),
	}, nil
}

// Edit replaces old text in a file. It needs no prior read (old_string
// anchors it), but it re-checks the file just before committing and
// returns a conflict if anything changed after its own read.
func (eng Engine) Edit(session, workspace, requestedPath, oldText, newText string, replaceAll bool) (EditResult, error) {
	if strings.TrimSpace(requestedPath) == "" {
		return EditResult{}, &EngineError{Kind: "message", Message: "edit: path is required"}
	}
	if oldText == "" {
		return EditResult{}, &EngineError{Kind: "message", Message: "edit: old_string is required and cannot be empty"}
	}
	if oldText == newText {
		return EditResult{}, &EngineError{Kind: "message", Message: "edit: old_string and new_string are identical; nothing to change"}
	}
	resolved, scope, err := ResolveScopedPath("edit", "path", requestedPath, workspace)
	if err != nil {
		return EditResult{}, &EngineError{Kind: "message", Message: err.Error()}
	}
	unlock := lockPath(resolved)
	defer unlock()
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return EditResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("edit: path %q does not exist", requestedPath)}
		}
		return EditResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("edit: path %q cannot be read: %s", requestedPath, syserr.Str(statErr))}
	}
	if info.IsDir() {
		return EditResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("edit: path %q is a directory, not a file", requestedPath)}
	}
	if !info.Mode().IsRegular() {
		return EditResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("edit: path %q is not a regular file", requestedPath)}
	}
	if info.Size() > MaxFileBytes {
		return EditResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("edit: file is %d bytes, over the %d byte limit", info.Size(), MaxFileBytes)}
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return EditResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("edit: path %q cannot be read: %s", requestedPath, syserr.Str(err))}
	}
	if hasNul(raw) {
		return EditResult{}, &EngineError{Kind: "message", Message: "edit: the file contains NUL bytes and is binary, not text; use a binary-aware tool instead"}
	}
	if !utf8.Valid(raw) {
		return EditResult{}, &EngineError{Kind: "message", Message: "edit: the file is not valid UTF-8; convert it to UTF-8 before editing"}
	}
	content := string(raw)
	strategy := MatchExact
	spans := exactSpans(content, oldText, newText)
	if len(spans) == 0 {
		strategy, spans = fuzzyMatch(content, oldText, newText)
	}
	if len(spans) == 0 {
		return EditResult{}, &EngineError{
			Kind:          "notfound",
			Message:       "edit: old text not found in file; read the file and copy the exact text",
			RequestedPath: requestedPath,
			ResolvedPath:  resolved,
			Scope:         scope,
		}
	}
	count := len(spans)
	if count > 1 && !replaceAll {
		occ := spanOccurrences(content, spans)
		if strategy == MatchExact {
			occ = FindOccurrences(content, oldText)
		}
		return EditResult{}, &EngineError{
			Kind:          "ambiguous",
			Message:       fmt.Sprintf("edit: old text occurs %d times; retry with more context around the one you want, or replace all", count),
			RequestedPath: requestedPath,
			ResolvedPath:  resolved,
			Scope:         scope,
			Count:         count,
			Occurrences:   occ,
		}
	}
	if !replaceAll {
		spans = spans[:1]
	}
	updated := applySpans(content, spans)
	if updated == content {
		return EditResult{}, &EngineError{Kind: "message", Message: "edit: old_string and new_string are identical (after matching); the file already has this text, nothing to change"}
	}
	perm := os.FileMode(0o644)
	if info.Mode().Perm() != 0 {
		perm = info.Mode().Perm()
	}
	before := hashBytes(raw)
	err = atomicReplace(resolved, []byte(updated), perm, func() error {
		if now, err := hashFile(resolved); err != nil || now != before {
			return errChanged
		}
		return nil
	})
	if errors.Is(err, errChanged) {
		fileVersions.forget(session, resolved)
		return EditResult{}, conflict("edit", ConflictChanged, requestedPath, resolved, scope)
	}
	if err != nil {
		return EditResult{}, &EngineError{Kind: "message", Message: fmt.Sprintf("edit: %s; the file was not changed", syserr.Str(err))}
	}
	// The session knows the new content only if it knew the old.
	if seen, ok := fileVersions.seenHash(session, resolved); ok && seen == before {
		fileVersions.record(session, resolved, hashBytes([]byte(updated)))
	} else {
		fileVersions.forget(session, resolved)
	}
	shown := spanOccurrences(content, spans)
	matches := make([]MatchSpan, 0, len(shown))
	for _, o := range shown {
		matches = append(matches, MatchSpan{StartLine: o.StartLine, EndLine: o.EndLine})
	}
	return EditResult{
		RequestedPath:  requestedPath,
		ResolvedPath:   resolved,
		Scope:          scope,
		Replacements:   len(spans),
		Bytes:          len(updated),
		Lines:          CountLines(updated),
		MatchStartLine: shown[0].StartLine,
		MatchEndLine:   shown[0].EndLine,
		Matches:        matches,
		Strategy:       strategy,
	}, nil
}

func exactSpans(content, oldText, newText string) []editSpan {
	var spans []editSpan
	for from := 0; ; {
		i := strings.Index(content[from:], oldText)
		if i < 0 {
			return spans
		}
		start := from + i
		spans = append(spans, editSpan{start: start, end: start + len(oldText), repl: newText})
		from = start + len(oldText)
	}
}

func FindOccurrences(content, needle string) []Occurrence {
	lines := strings.Split(content, "\n")
	lineStarts := make([]int, 0, len(lines))
	off := 0
	for _, line := range lines {
		lineStarts = append(lineStarts, off)
		off += len(line) + 1
	}
	var out []Occurrence
	searchFrom := 0
	for len(out) < strings.Count(content, needle) {
		idx := strings.Index(content[searchFrom:], needle)
		if idx < 0 {
			break
		}
		start := searchFrom + idx
		end := start + len(needle)
		startLine := lineOf(lineStarts, start)
		endOffset := end - 1
		if endOffset < 0 {
			endOffset = 0
		}
		endLine := lineOf(lineStarts, endOffset)
		out = append(out, Occurrence{
			Occurrence: len(out) + 1,
			StartLine:  startLine,
			EndLine:    endLine,
			Before:     nearestNonBlank(lines, startLine-1, -1),
			After:      nearestNonBlank(lines, endLine+1, 1),
		})
		searchFrom = end
	}
	return out
}

func lineOf(lineStarts []int, offset int) int {
	lo, hi := 0, len(lineStarts)-1
	if hi < 0 {
		return 1
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStarts[mid] <= offset {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

func nearestNonBlank(lines []string, start, dir int) *ContextLine {
	i := start
	for i >= 1 && i <= len(lines) {
		text := strings.TrimRight(lines[i-1], "\r")
		if strings.TrimSpace(text) != "" {
			return &ContextLine{Line: i, Text: text}
		}
		i += dir
	}
	return nil
}

func CountLines(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}

func hasNul(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}
