// read.go reads a text file with line ranges and caps.

package tools

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/media"
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
)

type ReadResponse struct {
	RequestedPath string `json:"requested_path"`
	ResolvedPath  string `json:"resolved_path"`
	Scope         string `json:"scope"`
	Content       string `json:"content"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	HasMore       bool   `json:"has_more"`
	// NextOffset is the offset that continues the read; 0 when HasMore
	// is false.
	NextOffset int    `json:"next_offset,omitempty"`
	StopReason string `json:"stop_reason"`
}

type ReadTool struct{}

func (ReadTool) Name() string { return "read" }

func (ReadTool) ParallelSafe() bool { return true }

func (ReadTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "read",
		Description: "Read a text file or PNG, JPEG, GIF or WebP image from disk. Images are sent visually to the model. Pass path as a full " +
			"absolute path; bare relative paths are rejected. " +
			"The reply holds the file content, the line range that " +
			"was returned, and whether more lines follow: when has_more is true, " +
			"call read again with offset set to next_offset. Reads outside the workspace are allowed but reported as outside. " +
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

func (t ReadTool) CallRich(raw json.RawMessage, ctx *ToolCtx) (RichOutput, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return RichOutput{}, ArgsError(fmt.Sprintf("read: the arguments are not valid JSON: %v", err))
	}
	if strings.TrimSpace(args.Path) == "" {
		return RichOutput{}, ArgsError("read: path is required")
	}
	resolved, scope, err := ResolveScopedPath("read", "path", args.Path, ctx.Workspace)
	if err != nil {
		return RichOutput{}, ArgsError(err.Error())
	}
	ref, image, err := media.ReadIfImage(resolved)
	if err != nil {
		return RichOutput{}, ArgsError(fmt.Sprintf("read: image: %v", err))
	}
	if image {
		data, err := jsonx.Marshal(map[string]any{
			"requested_path": args.Path, "resolved_path": resolved, "scope": scope,
			"mime_type": ref.MIMEType, "size": ref.Size, "content": "[image attached]",
		})
		if err != nil {
			return RichOutput{}, ArgsError(fmt.Sprintf("read: encode image metadata: %v", err))
		}
		return RichOutput{Content: string(data), Image: ref}, nil
	}
	content, err := t.Call(raw, ctx)
	return RichOutput{Content: content}, err
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
	page, rerr := readRange(resolved, offset, limit)
	if rerr != nil {
		return "", ArgsError(rerr.Error())
	}
	fileVersions.notePage(ctx.SessionID, resolved, info, offset, page.end, page.hasMore, page.clipped, page.hash)
	next := 0
	if page.hasMore {
		next = page.end + 1
	}
	out := ReadResponse{
		RequestedPath: args.Path,
		ResolvedPath:  resolved,
		Scope:         scope,
		Content:       page.content,
		StartLine:     page.start,
		EndLine:       page.end,
		HasMore:       page.hasMore,
		NextOffset:    next,
		StopReason:    page.reason,
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

// readPage is one page of a streamed read.
type readPageResult struct {
	content    string
	start, end int
	hasMore    bool
	reason     string
	// clipped: at least one returned line was truncated, so the page
	// does not show the file's full content.
	clipped bool
	// hash is the whole file's content hash, set only when the read
	// reached EOF.
	hash string
}

// readRange streams path line by line: memory stays bounded by the
// page and one capped line, whatever the file or line size. It stops
// as soon as the line after the page is seen, so hasMore is exact.
// NUL bytes are detected in everything scanned up to that point.
func readRange(path string, offset, limit int) (readPageResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return readPageResult{}, fmt.Errorf("read: %s", syserr.Str(err))
	}
	defer f.Close()
	h := sha256.New()
	br := bufio.NewReaderSize(io.TeeReader(f, h), 64*1024)
	binary := fmt.Errorf("read: the file contains NUL bytes and is binary, not text; use a binary-aware tool or convert it before reading")

	var page strings.Builder
	res := readPageResult{reason: "ok"}
	line := make([]byte, 0, 256)
	for lineNo := 1; ; lineNo++ {
		keep := lineNo >= offset
		line = line[:0]
		cut, sawAny, eof := false, false, false
		for {
			chunk, err := br.ReadSlice('\n')
			if len(chunk) > 0 {
				sawAny = true
				if bytes.IndexByte(chunk, 0) >= 0 {
					return readPageResult{}, binary
				}
				if keep {
					if room := maxLineBytes + utf8.UTFMax - len(line); room > 0 {
						if len(chunk) > room {
							chunk, cut = chunk[:room], true
						}
						line = append(line, chunk...)
					} else {
						cut = true
					}
				}
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if err == io.EOF {
				eof = true
			} else if err != nil {
				return readPageResult{}, fmt.Errorf("read: %s", syserr.Str(err))
			}
			break
		}
		if !sawAny {
			break
		}
		if keep {
			if res.start > 0 && res.end-res.start+1 >= limit {
				res.hasMore, res.reason = true, "line_limit"
				return withContent(res, &page), nil
			}
			text, marked := formatLine(line, cut)
			res.clipped = res.clipped || marked
			if res.start > 0 && page.Len()+len(text)+1 > MaxReadBytes {
				res.hasMore, res.reason = true, "byte_limit"
				return withContent(res, &page), nil
			}
			if res.start == 0 {
				res.start = lineNo
			}
			page.WriteString(text)
			page.WriteByte('\n')
			res.end = lineNo
		}
		if eof {
			break
		}
	}
	// Drain anything ReadSlice left buffered so the hash covers the file.
	if _, err := io.Copy(io.Discard, br); err != nil {
		return readPageResult{}, fmt.Errorf("read: %s", syserr.Str(err))
	}
	if res.start == 0 && offset > 1 {
		res.reason = "past_end"
	}
	res.hash = hex.EncodeToString(h.Sum(nil))
	return withContent(res, &page), nil
}

func withContent(res readPageResult, page *strings.Builder) readPageResult {
	res.content = page.String()
	return res
}

// formatLine trims the newline and CR, caps bytes and runes, and marks
// a cut line.
func formatLine(raw []byte, cut bool) (string, bool) {
	raw = bytes.TrimSuffix(raw, []byte("\n"))
	if !cut {
		raw = bytes.TrimSuffix(raw, []byte("\r"))
	}
	text, over := capLineBytes(string(raw))
	text = strings.TrimRight(text, "\r")
	if capped, long := capRunes(text); long || over || cut {
		return capped + truncatedMarker, true
	}
	return text, false
}
