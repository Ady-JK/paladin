package jsonx

import (
	"strings"
	"testing"
)

func TestMarshalIsCompactWithoutTrailingNewline(t *testing.T) {
	out, err := Marshal(map[string]any{"a": 1, "b": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"a":1,"b":"x"}` {
		t.Fatalf("got %q", out)
	}
	if strings.HasSuffix(string(out), "\n") {
		t.Fatalf("trailing newline in %q", out)
	}
}

func TestMarshalDoesNotEscapeHTML(t *testing.T) {
	out, err := Marshal(map[string]string{"html": "<a href='x'>&</a>"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"html":"<a href='x'>&</a>"}`
	if string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if strings.Contains(string(out), `\u003c`) || strings.Contains(string(out), `\u0026`) {
		t.Fatalf("HTML escaped: %q", out)
	}
}

func TestMarshalPreservesUnicode(t *testing.T) {
	out, err := Marshal("héllo → 世界")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"héllo → 世界"` {
		t.Fatalf("got %q", out)
	}
}

func TestMarshalIndentEndsWithNewline(t *testing.T) {
	out, err := MarshalIndent(map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("got %q", out)
	}
}

func TestMarshalIndentDoesNotEscapeHTML(t *testing.T) {
	out, err := MarshalIndent(map[string]string{"x": "<&>"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<&>") {
		t.Fatalf("HTML was escaped: %q", out)
	}
}

func TestMarshalErrorForUnsupportedValue(t *testing.T) {
	if _, err := Marshal(make(chan int)); err == nil {
		t.Fatal("expected marshal error")
	}
	if _, err := MarshalIndent(make(chan int)); err == nil {
		t.Fatal("expected indent marshal error")
	}
}

func TestMustMarshalFallsBackToEmptyObject(t *testing.T) {
	if got := string(MustMarshal(make(chan int))); got != "{}" {
		t.Fatalf("got %q", got)
	}
	if got := string(MustMarshalIndent(make(chan int))); got != "{}" {
		t.Fatalf("got %q", got)
	}
}

func TestMustMarshalRoundTrips(t *testing.T) {
	if got := string(MustMarshal(map[string]int{"n": 2})); got != `{"n":2}` {
		t.Fatalf("got %q", got)
	}
	if got := string(MustMarshalIndent(map[string]int{"n": 2})); got != "{\n  \"n\": 2\n}\n" {
		t.Fatalf("got %q", got)
	}
}
