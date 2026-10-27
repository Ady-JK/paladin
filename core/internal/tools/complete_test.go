package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeCompleteFixture(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	files := map[string]string{
		"src/main.go":       "package main\n",
		"src/server/app.go": "package server\n",
		"README.md":         "# hi\n",
		".hidden":           "secret\n",
		".gitignore":        "ignored/\n*.log\n/build\n",
		"ignored/x.go":      "package x\n",
		"debug.log":         "log\n",
		"build/out.bin":     "bin",
		"notes/todo.txt":    "todo\n",
	}
	for name, content := range files {
		p := filepath.Join(ws, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A sibling outside the workspace for ../ completion.
	sib := filepath.Join(filepath.Dir(ws), "sibling")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sib, "outside.txt"), []byte("out\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

func completePaths(entries []FileEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func containsPath(entries []FileEntry, want string) bool {
	for _, e := range entries {
		if e.Path == want {
			return true
		}
	}
	return false
}

func TestCompleteListsWorkspace(t *testing.T) {
	ws := writeCompleteFixture(t)
	got, err := CompleteFiles(ws, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"src/main.go", "src/server/app.go", "README.md", "notes/todo.txt"} {
		if !containsPath(got, want) {
			t.Fatalf("missing %q in %v", want, completePaths(got))
		}
	}
}

func TestCompleteNeverSuggestsGitignored(t *testing.T) {
	ws := writeCompleteFixture(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// ripgrep honors .gitignore only inside a git repo.
	if out, err := exec.Command("git", "init", "-q", ws).CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v %s", err, out)
	}
	// Empty query and prefix queries, rg and fallback alike, must
	// never surface ignored paths.
	for _, q := range []string{"", "src", "i", "x", "l", "b", "debug"} {
		got, err := CompleteFiles(ws, q, 50)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range completePaths(got) {
			if strings.Contains(p, "ignored/") || strings.HasSuffix(p, ".log") || strings.HasPrefix(p, "build/") {
				t.Fatalf("query %q suggested gitignored %q", q, p)
			}
		}
	}
}

func TestCompletePrefixMatching(t *testing.T) {
	ws := writeCompleteFixture(t)
	got, err := CompleteFiles(ws, "src/ma", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "src/main.go") {
		t.Fatalf("got %v", completePaths(got))
	}
	if containsPath(got, "src/server/app.go") {
		t.Fatalf("over-matched: %v", completePaths(got))
	}
	// Basename match across directories.
	got, err = CompleteFiles(ws, "app", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "src/server/app.go") {
		t.Fatalf("got %v", completePaths(got))
	}
}

func TestCompleteDotDotOutside(t *testing.T) {
	ws := writeCompleteFixture(t)
	got, err := CompleteFiles(ws, "../sib", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "../sibling/outside.txt") {
		t.Fatalf("got %v", completePaths(got))
	}
	for _, e := range got {
		if e.Path == "../sibling/outside.txt" && !e.Outside {
			t.Fatal("outside file not flagged")
		}
	}
	// Backslash separators work the same (Windows chat style).
	got, err = CompleteFiles(ws, `..\sib`, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "../sibling/outside.txt") {
		t.Fatalf("backslash query failed: %v", completePaths(got))
	}
}

func TestCompleteFallbackWalkHonorsGitignore(t *testing.T) {
	ws := writeCompleteFixture(t)
	// Force the walk fallback by pointing PATH at nothing.
	t.Setenv("PATH", "")
	got, err := CompleteFiles(ws, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "src/main.go") {
		t.Fatalf("got %v", completePaths(got))
	}
	if !containsPath(got, ".hidden") {
		t.Fatalf("fallback should include hidden files, got %v", completePaths(got))
	}
	for _, p := range completePaths(got) {
		if strings.Contains(p, "ignored/") || strings.HasSuffix(p, ".log") || strings.HasPrefix(p, "build/") {
			t.Fatalf("fallback suggested gitignored %q", p)
		}
	}
}

func TestCompleteOffersFolders(t *testing.T) {
	ws := writeCompleteFixture(t)
	got, err := CompleteFiles(ws, "src/", 50)
	if err != nil {
		t.Fatal(err)
	}
	// The folder itself leads so `@src/` tags it.
	if len(got) == 0 || got[0].Path != "src/" || !got[0].IsDir {
		t.Fatalf("folder itself should lead, got %v", completePaths(got))
	}
	if !containsPath(got, "src/server/") {
		t.Fatalf("subfolder missing in %v", completePaths(got))
	}
	if !containsPath(got, "src/main.go") {
		t.Fatalf("file under folder missing in %v", completePaths(got))
	}
	for _, e := range got {
		if e.ResolvedPath == "" {
			t.Fatalf("missing resolved path in %+v", e)
		}
	}
	// Gitignored folders never surface.
	if containsPath(got, "ignored/") {
		t.Fatalf("gitignored folder offered in %v", completePaths(got))
	}
	// Fully typed folder path resolves even without a trailing slash.
	got, err = CompleteFiles(ws, "src", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "src/") {
		t.Fatalf("typed folder should resolve, got %v", completePaths(got))
	}
	// Fully typed file path leads.
	got, err = CompleteFiles(ws, "src/main.go", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Path != "src/main.go" || got[0].IsDir {
		t.Fatalf("exact file should lead, got %v", completePaths(got))
	}
	// Resolved paths are OS-absolute: the composer inserts them.
	wsAbs, _ := filepath.Abs(ws)
	_ = wsAbs
	for _, e := range got {
		if !filepath.IsAbs(e.ResolvedPath) {
			t.Fatalf("resolved path not absolute: %+v", e)
		}
	}
	// Outside folders complete with ../ and flag Outside.
	got, err = CompleteFiles(ws, "../", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "../sibling/") {
		t.Fatalf("outside folder missing in %v", completePaths(got))
	}
	for _, e := range got {
		if e.Path == "../sibling/" && (!e.IsDir || !e.Outside) {
			t.Fatalf("outside folder misflagged: %+v", e)
		}
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"*.log", "debug.log", true},
		{"*.log", "a/debug.log", false}, // segment match only; anchoring handled by caller
		{"*.go", "main.go", true},
		{"?", "a", true},
		{"?", "ab", false},
		{"build", "build", true},
		{"**", "a/b/c", true},
		{"src/**", "src/a/b", true},
		{"src/**", "other/a", false},
	}
	for _, tc := range cases {
		got, err := wildcardMatch(tc.pat, tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("wildcardMatch(%q,%q)=%v want %v", tc.pat, tc.name, got, tc.want)
		}
	}
	// Anchored rule matching uses full relative paths.
	if !matchIgnoreRule(ignoreRule{pattern: "build", anchored: true}, "build/out.bin", false) {
		t.Fatal("anchored build should ignore build/out.bin")
	}
	if matchIgnoreRule(ignoreRule{pattern: "build", anchored: true}, "src/build/x", false) {
		t.Fatal("anchored build should not ignore src/build/x")
	}
	if !matchIgnoreRule(ignoreRule{pattern: "*.log"}, "debug.log", false) {
		t.Fatal("*.log should ignore debug.log")
	}
	if matchIgnoreRule(ignoreRule{pattern: "*.log"}, "sub/ok.txt", false) {
		t.Fatal("*.log should not ignore sub/ok.txt")
	}
	if !matchIgnoreRule(ignoreRule{pattern: "ignored", dirOnly: true}, "ignored/x.go", false) {
		t.Fatal("ignored/ should ignore its subtree")
	}
}

func TestFuzzyScore(t *testing.T) {
	// Empty query matches everything.
	if _, ok := fuzzyScore("", "anything"); !ok {
		t.Fatal("empty query should match")
	}
	// Non-subsequences never match.
	for _, q := range []string{"zzz", "mnqx", "main.gox"} {
		if _, ok := fuzzyScore(q, "src/main.go"); ok {
			t.Fatalf("%q should not match", q)
		}
	}
	// Contiguous and separator-anchored matches beat scattered ones.
	mn, _ := fuzzyScore("main", "src/main.go")
	scattered, ok := fuzzyScore("main", "my_awesome_notes_on_integration.md")
	if !ok {
		t.Fatal("scattered should still match")
	}
	if mn >= scattered {
		t.Fatalf("contiguous %d should beat scattered %d", mn, scattered)
	}
	// Basename start beats deep-path scatter.
	if a, _ := fuzzyScore("app", "app.go"); true {
		if b, _ := fuzzyScore("app", "x/maps/and/plans.md"); a >= b {
			t.Fatalf("basename %d should beat scatter %d", a, b)
		}
	}
}

func TestCompleteFuzzyMatching(t *testing.T) {
	ws := writeCompleteFixture(t)
	// Non-contiguous query still finds the file.
	got, err := CompleteFiles(ws, "sma", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, "src/main.go") {
		t.Fatalf("fuzzy sma missed main.go in %v", completePaths(got))
	}
	// Basename hits rank first through the caller boost.
	got, err = CompleteFiles(ws, "app", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Path != "src/server/app.go" {
		t.Fatalf("basename should lead, got %v", completePaths(got))
	}
}
