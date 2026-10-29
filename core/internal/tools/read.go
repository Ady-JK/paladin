// Package tools: read.go reads a text file with line ranges and caps.
package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

const (
	DefaultReadLimit = 2000
	MaxReadBytes     = 50 * 1024
)

const (
	maxLineRunes    = 2000
	maxLineBytes    = maxLineRunes * 4
	truncatedMarker = " … [truncated]"
	headBytes       = 8 * 1024
)

type ReadResponse struct {
	RequestedPath string `json:"requested_path"`
	ResolvedPath  string `json:"resolved_path"`
	Scope         string `json:"scope"`
	Content       string `json:"content"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	HasMore       bool   `json:"has_more"`
	StopReason    string `json:"stop_reason"`
}

type ReadTool struct{}

func (ReadTool) Name() string { return "read" }

func (ReadTool) ParallelSafe() bool { return true }

func (ReadTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "read",
		Description: "Read a text file from disk. Pass path as a full " +
			"absolute path; bare relative paths are rejected. " +
			"The reply holds the file content, the line range that " +
			"was returned, and whether more lines follow: continue with a larger offset " +
			"when it does. Reads outside the workspace are allowed but reported as outside. " +
			"Use read to inspect a file before describing or changing it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "The file to read as a full absolute path."},
				"offset": map[string]any{"type": "integer", "description": "The first line to read, 1-based. Defaults to 1.", "minimum": 1},
				"limit":  map[string]any{"type": "integer", "description": "How many lines to read. Defaults to 2000; the content is also capped at 50 KB.", "minimum": 1},
			},
			"required": []string{"path"},
		},
	}
}

func (ReadTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Limit  int64  `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("read: the arguments are not valid JSON: %v", err))
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", ArgsError("read: path is required")
	}
	if args.Offset < 0 {
		return "", ArgsError("read: offset is 1-based and cannot be negative")
	}
	if args.Limit < 0 {
		return "", ArgsError("read: limit cannot be negative")
	}
	offset := 1
	if args.Offset != 0 {
		offset = int(args.Offset)
	}
	limit := DefaultReadLimit
	if args.Limit != 0 {
		limit = int(args.Limit)
	}
	resolved, scope, err := ResolveScopedPath("read", "path", args.Path, ctx.Workspace)
	if err != nil {
		return "", ArgsError(err.Error())
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return "", ArgsError(fmt.Sprintf("read: path %q does not exist", args.Path))
		} else if os.IsPermission(statErr) {
			return "", ArgsError(fmt.Sprintf("read: path %q cannot be read: permission denied", args.Path))
		}
		return "", ArgsError(fmt.Sprintf("read: path %q cannot be read: %s", args.Path, syserr.Str(statErr)))
	}
	if info.IsDir() {
		return "", ArgsError(fmt.Sprintf("read: path %q is a directory, not a file", args.Path))
	}
	if !info.Mode().IsRegular() {
		return "", ArgsError(fmt.Sprintf("read: path %q is not a regular file", args.Path))
	}
	content, start, end, hasMore, reason, rerr := readRange(resolved, offset, limit)
	if rerr != nil {
		return "", ArgsError(rerr.Error())
	}
	out := ReadResponse{
		RequestedPath: args.Path,
		ResolvedPath:  resolved,
		Scope:         scope,
		Content:       content,
		StartLine:     start,
		EndLine:       end,
		HasMore:       hasMore,
		StopReason:    reason,
	}
	data, err := jsonx.Marshal(out)
	if err != nil {
		return "", ArgsError(fmt.Sprintf("read: encode the result: %v", err))
	}
	return string(data), nil
}

func capLineBytes(raw string) (string, bool) {
	text := strings.ToValidUTF8(raw, "�")
	if len(text) <= maxLineBytes {
		return text, false
	}
	return strings.ToValidUTF8(truncateUTF8(raw, maxLineBytes), "�"), true
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	end := max
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end]
}

func capRunes(line string) (string, bool) {
	runes := []rune(line)
	if len(runes) <= maxLineRunes {
		return line, false
	}
	return string(runes[:maxLineRunes]), true
}

func readRange(path string, offset, limit int) (string, int, int, bool, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, false, "", fmt.Errorf("read: %s", syserr.Str(err))
	}
	defer f.Close()
	head := make([]byte, headBytes)
	n := 0
	for n < headBytes {
		k, err := f.Read(head[n:])
		if k > 0 {
			n += k
		}
		if err != nil {
			break
		}
	}
	head = head[:n]
	if hasNul(head) {
		return "", 0, 0, false, "", fmt.Errorf("read: the file is binary, not text")
	}
	rest, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, false, "", fmt.Errorf("read: %s", syserr.Str(err))
	}
	// Rust chains the 8KB head back with the rest; reading the whole
	// file at once is equivalent (head + rest == full content).
	data := rest
	var content strings.Builder
	lineNo := 0
	start, end := 0, 0
	hasMore := false
	reason := "ok"
	for _, raw := range strings.SplitAfter(string(data), "\n") {
		if strings.HasSuffix(raw, "\n") {
			raw = raw[:len(raw)-1]
		} else if raw == "" {
			continue
		} else {
			// Last line without trailing newline: Rust split(b'\n')
			// yields it as a final item too.
		}
		lineNo++
		if lineNo < offset {
			continue
		}
		text, cut := capLineBytes(raw)
		text = strings.TrimRight(text, "\r")
		if capped, over := capRunes(text); over || cut {
			text = capped + truncatedMarker
		} else if cut {
			text = text + truncatedMarker
		}
		if start > 0 {
			if end-start+1 >= limit {
				hasMore = true
				reason = "line_limit"
				break
			}
			if content.Len()+len(text)+1 > MaxReadBytes {
				hasMore = true
				reason = "byte_limit"
				break
			}
		}
		if start == 0 {
			start = lineNo
		}
		content.WriteString(text)
		content.WriteByte('\n')
		end = lineNo
	}
	_ = f
	return content.String(), start, end, hasMore, reason, nil
}
