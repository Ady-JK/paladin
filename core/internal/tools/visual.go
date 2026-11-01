// visual.go is the visual tool: the model hands over an SVG or an HTML
// fragment (a diagram, a chart, a small interactive widget) and the app
// draws it inline in the chat. The app renders it in a sandboxed frame
// with its own origin and no network, themed with Paladin's colors. The
// tool only checks the code and keeps a copy in the session folder.

package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

const (
	// VisualDir holds each session's visuals, one file per call.
	VisualDir      = "visuals"
	visualMaxBytes = 256 << 10
	visualMaxTitle = 80
)

type VisualTool struct{}

type visualArgs struct {
	Title   string `json:"title"`
	Code    string `json:"code"`
	Caption string `json:"caption"`
}

// VisualResponse is what the model gets back.
type VisualResponse struct {
	Title  string `json:"title"`
	Format string `json:"format"`
	Bytes  int    `json:"bytes"`
	Path   string `json:"path,omitempty"`
	Note   string `json:"note"`
}

func (VisualTool) Name() string { return "visual" }

func (VisualTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "visual",
		Description: "Show the user a visual inline in the chat: a diagram, chart, table, mockup or small interactive widget. " +
			"Pass SVG (code starting with <svg) or an HTML fragment (no <html>, <head> or <body>; <style> and <script> are fine). " +
			"It renders in a sandboxed frame with no network: everything must be inline (no external scripts, fonts, images or fetch). " +
			"Use the theme variables so it matches the app in light and dark: var(--v-bg) (transparent page), var(--v-surface), " +
			"var(--v-fg), var(--v-muted), var(--v-border), var(--v-accent), var(--v-good), var(--v-warn), var(--v-bad), and " +
			"var(--v-series-1) … var(--v-series-6) for chart series; font var(--v-font) and var(--v-mono). Make it fluid " +
			"(width 100%, SVG with a viewBox) and keep text at 12px or larger. A script may call paladin.sendPrompt(text) to put " +
			"text into the user's message box (they decide whether to send it). Use it when a picture explains better than " +
			"prose; do not use it for plain text answers.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":   map[string]any{"type": "string", "description": "Short title shown above the visual, e.g. \"Request flow\"."},
				"code":    map[string]any{"type": "string", "description": "The SVG or HTML fragment, at most 256 KB."},
				"caption": map[string]any{"type": "string", "description": "Optional one-line caption under the visual."},
			},
			"required": []string{"title", "code"},
		},
	}
}

// VisualFormat is "svg" for SVG code and "html" otherwise.
func VisualFormat(code string) string {
	head := strings.ToLower(strings.TrimSpace(code))
	head = strings.TrimSpace(strings.TrimPrefix(head, "<?xml"))
	if strings.HasPrefix(head, "<svg") || (strings.HasPrefix(code, "<?xml") && strings.Contains(head, "<svg")) {
		return "svg"
	}
	return "html"
}

func (VisualTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var a visualArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", ArgsError("visual: invalid arguments: " + err.Error())
	}
	a.Title = strings.TrimSpace(a.Title)
	code := strings.TrimSpace(a.Code)
	switch {
	case a.Title == "":
		return "", ArgsError("visual: title is required")
	case utf8.RuneCountInString(a.Title) > visualMaxTitle:
		return "", ArgsError(fmt.Sprintf("visual: title is longer than %d characters", visualMaxTitle))
	case code == "":
		return "", ArgsError("visual: code is required")
	case len(code) > visualMaxBytes:
		return "", ArgsError(fmt.Sprintf("visual: code is %d bytes; keep it under %d KB", len(code), visualMaxBytes>>10))
	case !strings.Contains(code, "<"):
		return "", ArgsError("visual: code must be SVG or HTML markup")
	}
	if err := fullDocument(code); err != nil {
		return "", ArgsError("visual: " + err.Error())
	}
	format := VisualFormat(code)
	out := VisualResponse{Title: a.Title, Format: format, Bytes: len(code),
		Note: "Shown to the user inline. Do not repeat its content in prose; refer to it briefly."}
	if ctx != nil && ctx.SessionDir != "" && ctx.CallID != "" {
		path := filepath.Join(ctx.SessionDir, VisualDir, safeCallFile(ctx.CallID)+"."+format)
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil && atomicfile.Write(path, []byte(code)) == nil {
			out.Path = path
		}
	}
	return jsonResult(out)
}

// fullDocument rejects whole documents: the app supplies the page.
func fullDocument(code string) error {
	lower := strings.ToLower(code)
	for _, tag := range []string{"<!doctype", "<html", "<head", "<body"} {
		if strings.Contains(lower, tag) {
			return errors.New("send a fragment without " + tag + ">; the app provides the page, theme and sizing")
		}
	}
	return nil
}

// safeCallFile turns a call id into a file name.
func safeCallFile(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "visual"
	}
	return b.String()
}
