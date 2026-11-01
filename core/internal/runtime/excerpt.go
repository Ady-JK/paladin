// excerpt.go serves short read-only file excerpts to the UI, such as
// the code around a review finding. Only files inside the session's
// workspace are served, never binary or oversized ones.

package runtime

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

const (
	excerptContext  = 3
	excerptMaxLines = 60
	excerptMaxLine  = 400
)

// FileExcerpt is a window of a file's lines.
type FileExcerpt struct {
	Path      string   `json:"path"`
	Rel       string   `json:"rel"`
	StartLine int      `json:"startLine"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// FileExcerpt returns lines start..end of path with a little context.
func (r *AgentRuntime) FileExcerpt(sessionID, cwd, path string, start, end int) (FileExcerpt, error) {
	sess, err := r.transcripts.Load(cwd, sessionID)
	if err != nil {
		return FileExcerpt{}, err
	}
	ws := sess.Meta.Cwd
	resolved, scope, err := tools.ResolveScopedPath("fileExcerpt", "path", path, ws)
	if err != nil {
		return FileExcerpt{}, err
	}
	if scope != tools.ScopeInside {
		return FileExcerpt{}, fmt.Errorf("fileExcerpt: %s is outside the session workspace", resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return FileExcerpt{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > tools.MaxFileBytes {
		return FileExcerpt{}, fmt.Errorf("fileExcerpt: %s is not a readable text file", resolved)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return FileExcerpt{}, err
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return FileExcerpt{}, fmt.Errorf("fileExcerpt: %s is binary", resolved)
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	start = max(1, start)
	end = max(start, end)
	from := max(1, start-excerptContext)
	to := min(len(lines), end+excerptContext)
	ex := FileExcerpt{Path: resolved, StartLine: from, Lines: []string{}}
	if rel, err := filepath.Rel(ws, resolved); err == nil {
		ex.Rel = filepath.ToSlash(rel)
	}
	if to-from+1 > excerptMaxLines {
		to, ex.Truncated = from+excerptMaxLines-1, true
	}
	for i := from; i <= to && i <= len(lines); i++ {
		line := lines[i-1]
		if r := []rune(line); len(r) > excerptMaxLine {
			line = string(r[:excerptMaxLine]) + "…"
		}
		ex.Lines = append(ex.Lines, line)
	}
	return ex, nil
}
