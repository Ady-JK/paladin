package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// searchEngines runs a test once per engine: the built-in walk always,
// ripgrep only when it is installed.
func searchEngines(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	engines := []string{"builtin"}
	if _, err := LookCommand("rg"); err == nil {
		engines = append(engines, "rg")
	}
	for _, engine := range engines {
		t.Run(engine, func(t *testing.T) {
			prev := searchUseRipgrep
			searchUseRipgrep = engine == "rg"
			t.Cleanup(func() { searchUseRipgrep = prev })
			fn(t)
		})
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func callSearch(t *testing.T, tool Tool, ctx *ToolCtx, args map[string]any) SearchResponse {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := tool.Call(raw, ctx)
	if err != nil {
		t.Fatalf("%s %v: %v", tool.Name(), args, err)
	}
	var resp SearchResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return resp
}

func TestGrepFindsMatchesWithLineNumbers(t *testing.T) {
	searchEngines(t, func(t *testing.T) {
		ctx, ws := testCtx(t)
		writeTree(t, ws, map[string]string{
			"a.go":                 "package a\nfunc Hello() {}\n",
			"sub/b.go":             "package sub\n// hello there\n",
			"node_modules/x/c.js":  "hello from deps\n",
			".git/HEAD":            "hello\n",
			"notes.txt":            "nothing here\n",
			"sub/deep/HelloAll.md": "HELLO\n",
		})
		resp := callSearch(t, GrepTool{}, ctx, map[string]any{"pattern": "hello", "ignore_case": true})
		if resp.Matches != 3 || resp.Files != 3 || resp.Truncated {
			t.Fatalf("got %+v", resp)
		}
		for _, want := range []string{filepath.Join(ws, "a.go"), "2: func Hello() {}", "2: // hello there"} {
			if !strings.Contains(resp.Output, want) {
				t.Errorf("output missing %q:\n%s", want, resp.Output)
			}
		}
		if strings.Contains(resp.Output, "node_modules") || strings.Contains(resp.Output, ".git") {
			t.Errorf("noise dirs searched:\n%s", resp.Output)
		}
		if resp.Scope != ScopeInside {
			t.Errorf("scope %q", resp.Scope)
		}
	})
}

func TestGrepFiltersAndLimits(t *testing.T) {
	searchEngines(t, func(t *testing.T) {
		ctx, ws := testCtx(t)
		writeTree(t, ws, map[string]string{
			"a.go":  "x1\nx2\nx3\n",
			"b.txt": "x4\n",
		})
		resp := callSearch(t, GrepTool{}, ctx, map[string]any{"pattern": `x\d`, "glob": "*.go"})
		if resp.Matches != 3 || strings.Contains(resp.Output, "b.txt") {
			t.Fatalf("glob filter: %+v", resp)
		}
		resp = callSearch(t, GrepTool{}, ctx, map[string]any{"pattern": `x\d`, "limit": 2})
		if resp.Matches != 2 || !resp.Truncated {
			t.Fatalf("limit: %+v", resp)
		}
		resp = callSearch(t, GrepTool{}, ctx, map[string]any{"pattern": `x\d`, "output": "files"})
		if resp.Files != 2 || strings.Contains(resp.Output, "x1") {
			t.Fatalf("files mode: %+v", resp)
		}
		resp = callSearch(t, GrepTool{}, ctx, map[string]any{"pattern": "x2", "context": 1, "path": filepath.Join(ws, "a.go")})
		for _, want := range []string{"1- x1", "2: x2", "3- x3"} {
			if !strings.Contains(resp.Output, want) {
				t.Errorf("context missing %q:\n%s", want, resp.Output)
			}
		}
	})
}

func TestGrepRejectsBadInput(t *testing.T) {
	searchEngines(t, func(t *testing.T) {
		ctx, ws := testCtx(t)
		for _, args := range []string{
			`{"pattern":""}`,
			`{"pattern":"a(","path":"` + ws + `"}`,
			`{"pattern":"a","path":"rel/dir"}`,
			`{"pattern":"a","path":"` + filepath.Join(ws, "missing") + `"}`,
		} {
			if _, err := (GrepTool{}).Call(json.RawMessage(args), ctx); err == nil {
				t.Errorf("%s: expected an error", args)
			}
		}
	})
}

func TestGrepSkipsBinaryAndSymlinkedFiles(t *testing.T) {
	ctx, ws := testCtx(t)
	outside := filepath.Join(filepath.Dir(ws), "secret.txt")
	writeTree(t, filepath.Dir(ws), map[string]string{"secret.txt": "needle\n"})
	writeTree(t, ws, map[string]string{"bin.dat": "needle\x00\x01"})
	if err := os.Symlink(outside, filepath.Join(ws, "link.txt")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	searchEngines(t, func(t *testing.T) {
		resp := callSearch(t, GrepTool{}, ctx, map[string]any{"pattern": "needle"})
		if resp.Matches != 0 {
			t.Fatalf("binary or symlink searched: %+v", resp)
		}
	})
}

func TestGrepHonorsGitignoreInRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	searchEngines(t, func(t *testing.T) {
		ctx, ws := testCtx(t)
		if out, err := exec.Command("git", "-C", ws, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
		writeTree(t, ws, map[string]string{
			".gitignore":    "build/\n",
			"src/a.go":      "needle\n",
			"build/out.txt": "needle\n",
		})
		resp := callSearch(t, GrepTool{}, ctx, map[string]any{"pattern": "needle"})
		if resp.Matches != 1 || strings.Contains(resp.Output, "build") {
			t.Fatalf("ignored file searched: %+v", resp)
		}
	})
}

func TestGlobMatchesNestedFilesNewestFirst(t *testing.T) {
	searchEngines(t, func(t *testing.T) {
		ctx, ws := testCtx(t)
		writeTree(t, ws, map[string]string{
			"main.go":             "",
			"cmd/app/app.go":      "",
			"cmd/app/app_test.go": "",
			"web/index.ts":        "",
			"node_modules/m.go":   "",
		})
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(ws, "main.go"), old, old); err != nil {
			t.Fatal(err)
		}
		resp := callSearch(t, GlobTool{}, ctx, map[string]any{"pattern": "**/*.go"})
		lines := strings.Split(strings.TrimSpace(resp.Output), "\n")
		if resp.Files != 3 || len(lines) != 3 || lines[2] != filepath.Join(ws, "main.go") {
			t.Fatalf("got %+v", resp)
		}
		resp = callSearch(t, GlobTool{}, ctx, map[string]any{"pattern": "*_test.go"})
		if resp.Files != 1 || !strings.Contains(resp.Output, "app_test.go") {
			t.Fatalf("basename pattern: %+v", resp)
		}
		resp = callSearch(t, GlobTool{}, ctx, map[string]any{"pattern": "{web,cmd}/**/*.{ts,go}", "limit": 1})
		if resp.Files != 1 || !resp.Truncated {
			t.Fatalf("braces and limit: %+v", resp)
		}
	})
}

func TestListShowsTreeWithCounts(t *testing.T) {
	searchEngines(t, func(t *testing.T) {
		ctx, ws := testCtx(t)
		writeTree(t, ws, map[string]string{
			"go.mod":            "",
			"cmd/a/main.go":     "",
			"cmd/a/more/x.go":   "",
			"internal/b/b.go":   "",
			"node_modules/p.js": "",
		})
		resp := callSearch(t, ListTool{}, ctx, map[string]any{"depth": 2})
		for _, want := range []string{ws + string(filepath.Separator), "  cmd/ (2 files)", "    a/ (2 files)", "  go.mod", "  internal/ (1 file)"} {
			if !strings.Contains(resp.Output, want+"\n") {
				t.Errorf("missing %q:\n%s", want, resp.Output)
			}
		}
		if strings.Contains(resp.Output, "main.go") || strings.Contains(resp.Output, "node_modules") {
			t.Errorf("too deep or noise:\n%s", resp.Output)
		}
	})
}

func TestSearchToolsAreReadOnly(t *testing.T) {
	r := BuiltinRegistry()
	for _, name := range []string{"grep", "glob", "list"} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("%s not registered", name)
		}
		if !r.ParallelSafe(name) || r.Mutates(name) {
			t.Errorf("%s must be parallel-safe and non-mutating", name)
		}
	}
}

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.go", "a.go", true},
		{"**/*.go", "x/y/a.go", true},
		{"src/**", "src/a/b.c", true},
		{"src/*.go", "src/a/b.go", false},
		{"*.{ts,tsx}", "deep/c.tsx", true},
		{"a?c.txt", "abc.txt", true},
		{"[ab].md", "c.md", false},
		{"docs/", "docs/x.md", false},
	}
	for _, c := range cases {
		re, err := globRegexp(c.pattern)
		if err != nil {
			t.Fatalf("%s: %v", c.pattern, err)
		}
		if got := globMatches(re, c.pattern, c.path); got != c.want {
			t.Errorf("%s ~ %s = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}
