package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func callVisual(t *testing.T, ctx *ToolCtx, a map[string]any) (VisualResponse, error) {
	t.Helper()
	raw, _ := json.Marshal(a)
	out, err := VisualTool{}.Call(raw, ctx)
	var resp VisualResponse
	if err == nil {
		if jerr := json.Unmarshal([]byte(out), &resp); jerr != nil {
			t.Fatal(jerr)
		}
	}
	return resp, err
}

func TestVisualKeepsACopyAndDetectsTheFormat(t *testing.T) {
	ctx, _ := testCtx(t)
	ctx.CallID = "call/7"
	svg := `<svg viewBox="0 0 10 10"><rect width="10" height="10" fill="var(--v-accent)"/></svg>`
	resp, err := callVisual(t, ctx, map[string]any{"title": "Box", "code": svg})
	if err != nil || resp.Format != "svg" || resp.Bytes != len(svg) {
		t.Fatalf("svg: %v %+v", err, resp)
	}
	want := filepath.Join(ctx.SessionDir, VisualDir, "call_7.svg")
	if got, _ := os.ReadFile(want); resp.Path != want || string(got) != svg {
		t.Fatalf("copy at %q: %q", resp.Path, got)
	}
	ctx.CallID = "c8"
	html := `<style>.k{color:var(--v-fg)}</style><div class="k">42</div><script>paladin.sendPrompt("x")</script>`
	if resp, err := callVisual(t, ctx, map[string]any{"title": "KPI", "code": html, "caption": "Revenue"}); err != nil || resp.Format != "html" {
		t.Fatalf("html: %v %+v", err, resp)
	}
	if VisualFormat(`<?xml version="1.0"?><svg/>`) != "svg" || VisualFormat("  <SVG></SVG>") != "svg" {
		t.Fatal("xml-prefixed and upper-case SVG")
	}
}

func TestVisualRejectsBadInput(t *testing.T) {
	ctx, _ := testCtx(t)
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"code": "<svg/>"}, "title is required"},
		{map[string]any{"title": "x"}, "code is required"},
		{map[string]any{"title": "x", "code": "just words"}, "SVG or HTML"},
		{map[string]any{"title": "x", "code": "<html><body>hi</body></html>"}, "fragment"},
		{map[string]any{"title": strings.Repeat("t", 81), "code": "<svg/>"}, "title is longer"},
		{map[string]any{"title": "x", "code": "<div>" + strings.Repeat("a", visualMaxBytes) + "</div>"}, "under 256 KB"},
	} {
		if _, err := callVisual(t, ctx, c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: want %q, got %v", c.args["title"], c.want, err)
		}
	}
}
