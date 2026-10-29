package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editFixture(t *testing.T, content string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEditFuzzyCascade(t *testing.T) {
	cases := []struct {
		name, file, old, new, want, strategy string
	}{
		{
			name: "exact stays exact",
			file: "a\nb\nc\n", old: "b", new: "B", want: "a\nB\nc\n", strategy: "exact",
		},
		{
			name: "LF old_string in a CRLF file keeps CRLF",
			file: "one\r\ntwo\r\nthree\r\n", old: "one\ntwo\n", new: "uno\ndos\n",
			want: "uno\r\ndos\r\nthree\r\n", strategy: "line_endings",
		},
		{
			name: "trailing whitespace drift",
			file: "func a() {   \n\treturn 1\t\n}\n", old: "func a() {\n\treturn 1\n}", new: "func a() {\n\treturn 2\n}",
			want: "func a() {\n\treturn 2\n}\n", strategy: "whitespace",
		},
		{
			name: "smart quotes and dashes folded",
			file: "msg := \u201chello\u201d \u2014 world\n", old: `msg := "hello" - world`, new: `msg := "bye"`,
			want: "msg := \"bye\"\n", strategy: "whitespace",
		},
		{
			name: "indentation drift re-indents new_string",
			file: "class A:\n        def f(self):\n            return 1\n",
			old:  "def f(self):\n    return 1", new: "def f(self):\n    return 2\n\ndef g(self):\n    return 3",
			want:     "class A:\n        def f(self):\n            return 2\n\n        def g(self):\n            return 3\n",
			strategy: "indentation",
		},
		{
			name: "BOM preserved",
			file: "\ufeffhello world\n", old: "  hello world", new: "hi",
			want: "\ufeffhi\n", strategy: "indentation",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, path := editFixture(t, c.file)
			res, err := NewEngine().Edit(dir, path, c.old, c.new, false)
			if err != nil {
				t.Fatalf("edit failed: %v", err)
			}
			if got := readBack(t, path); got != c.want {
				t.Fatalf("content\n got %q\nwant %q", got, c.want)
			}
			if res.Strategy != c.strategy {
				t.Fatalf("strategy %q, want %q", res.Strategy, c.strategy)
			}
		})
	}
}

func TestEditFuzzyMatchMustBeUnique(t *testing.T) {
	// No exact hit; both lines match once trailing whitespace is ignored.
	dir, path := editFixture(t, "foo() \nfoo()\t\n")
	_, err := NewEngine().Edit(dir, path, "foo()\n", "bar()\n", false)
	var ee *EngineError
	if err == nil || !errorsAs(err, &ee) || ee.Kind != "ambiguous" || ee.Count != 2 {
		t.Fatalf("two fuzzy matches must be ambiguous, got %v", err)
	}
	if got := readBack(t, path); strings.Contains(got, "bar") {
		t.Fatalf("ambiguous edit must not write: %q", got)
	}
}

func TestEditFuzzyReplaceAll(t *testing.T) {
	dir, path := editFixture(t, "a = 1  \nb\na = 1\t\n")
	res, err := NewEngine().Edit(dir, path, "a = 1\n", "a = 2\n", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, path); got != "a = 2\nb\na = 2\n" || res.Replacements != 2 {
		t.Fatalf("got %q (%d replacements)", got, res.Replacements)
	}
}

func TestEditRejectsNoOp(t *testing.T) {
	dir, path := editFixture(t, "same\n")
	_, err := NewEngine().Edit(dir, path, "same", "same", false)
	if err == nil || !strings.Contains(err.Error(), "identical") {
		t.Fatalf("no-op edit must be rejected, got %v", err)
	}
}

func TestEditNotFoundStillFails(t *testing.T) {
	dir, path := editFixture(t, "alpha\nbeta\n")
	_, err := NewEngine().Edit(dir, path, "gamma", "delta", false)
	var ee *EngineError
	if err == nil || !errorsAs(err, &ee) || ee.Kind != "notfound" {
		t.Fatalf("got %v", err)
	}
}

func errorsAs(err error, target **EngineError) bool {
	e, ok := err.(*EngineError)
	if ok {
		*target = e
	}
	return ok
}

func TestEditToolReportsFuzzyStrategy(t *testing.T) {
	dir, path := editFixture(t, "x = 1   \n")
	out, err := NewEditTool(NewEngine()).Call([]byte(`{"path":`+jsonQuote(path)+`,"old_string":"x = 1\n","new_string":"x = 2\n"}`), &ToolCtx{Workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"match_strategy":"whitespace"`) || !strings.Contains(out, "note") {
		t.Fatalf("fuzzy match must be reported to the model: %s", out)
	}
	exact, _ := NewEditTool(NewEngine()).Call([]byte(`{"path":`+jsonQuote(path)+`,"old_string":"x = 2","new_string":"x = 3"}`), &ToolCtx{Workspace: dir})
	if strings.Contains(exact, "note") {
		t.Fatalf("exact match needs no note: %s", exact)
	}
}

func jsonQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
