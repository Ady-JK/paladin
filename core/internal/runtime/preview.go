// preview.go serves the short preview a tagged file shows in the
// composer: a few lines of text, or a small image as a data URL. It is
// read-only and scoped to the workspace, like fileExcerpt.

package runtime

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

const (
	// previewLines is how many non-empty lines a tag card shows.
	previewLines = 4
	// previewLineRunes caps one preview line; the card clips anyway, but
	// this keeps a minified or one-line file from being copied whole.
	previewLineRunes = 200
	// previewReadBytes bounds what the preview reads from a text file.
	previewReadBytes = 32 << 10
	// previewHeaderBytes is enough for the image signature check.
	previewHeaderBytes = 512
)

// FilePreview is one tagged item's composer preview.
type FilePreview struct {
	Path string `json:"path"`
	Rel  string `json:"rel"`
	// Kind is text, image, binary, dir, outside or missing.
	Kind  string   `json:"kind"`
	Lines []string `json:"lines,omitempty"`
	// DataURL is a small image's bytes, for the card thumbnail.
	DataURL   string `json:"dataUrl,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// FilePreview reads the start of path for the composer's tag card. Only
// files inside the workspace are previewed; a path outside it, a folder
// or a file that is gone still yields a kind, never an error, so the
// card always has something to show.
func (r *AgentRuntime) FilePreview(cwd, path string) (FilePreview, error) {
	ws := resolveCwd(r, cwd)
	resolved, scope, err := tools.ResolveScopedPath("filePreview", "path", path, ws)
	if err != nil {
		return FilePreview{}, err
	}
	view := FilePreview{Path: resolved, Kind: "text"}
	if rel, err := filepath.Rel(ws, resolved); err == nil {
		view.Rel = filepath.ToSlash(rel)
	} else {
		view.Rel = filepath.Base(resolved)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			view.Kind = "missing"
			return view, nil
		}
		return FilePreview{}, err
	}
	if scope != tools.ScopeInside {
		view.Kind = "outside"
		return view, nil
	}
	if info.IsDir() {
		view.Kind = "dir"
		return view, nil
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		view.Kind = "binary"
		return view, nil
	}
	f, err := os.Open(resolved)
	if err != nil {
		return FilePreview{}, err
	}
	defer f.Close()
	header := make([]byte, min(int(info.Size()), previewHeaderBytes))
	n, err := f.Read(header)
	if err != nil {
		return FilePreview{}, fmt.Errorf("filePreview: read %s: %w", resolved, err)
	}
	header = header[:n]
	if mime := media.MIME(header); mime != "" {
		view.Kind = "image"
		if info.Size() > media.MaxBytes {
			view.Truncated = true
			return view, nil
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return FilePreview{}, err
		}
		data, err := io.ReadAll(io.LimitReader(f, media.MaxBytes+1))
		if err != nil {
			return FilePreview{}, err
		}
		view.DataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		return view, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return FilePreview{}, err
	}
	data, err := io.ReadAll(io.LimitReader(f, previewReadBytes+1))
	if err != nil {
		return FilePreview{}, err
	}
	if len(data) > previewReadBytes {
		data = data[:previewReadBytes]
		view.Truncated = true
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		view.Kind = "binary"
		return view, nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if r := []rune(line); len(r) > previewLineRunes {
			line = string(r[:previewLineRunes]) + "…"
			view.Truncated = true
		}
		view.Lines = append(view.Lines, line)
		if len(view.Lines) == previewLines {
			break
		}
	}
	if len(view.Lines) == 0 {
		view.Kind = "binary"
	}
	return view, nil
}
