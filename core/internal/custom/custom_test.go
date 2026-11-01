package custom

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetDataDir(dir)
	t.Cleanup(func() { SetDataDir("") })
	return dir
}

func TestOverridesReplaceDefaultsUntilReset(t *testing.T) {
	Register(Item{ID: "test.greeting", Group: "Reminders", Title: "Greeting", Vars: []string{"name"},
		Default: func() string { return "Hello {{name}}." }})
	if got := TextFill("test.greeting", map[string]string{"name": "Ada"}); got != "Hello Ada." {
		t.Fatalf("no data folder: %q", got)
	}
	dir := withData(t)
	st, err := Set("test.greeting", "Hi there")
	if err != nil || !st.Customized || st.Value != "Hi there" || st.DefaultVal != "Hello {{name}}." {
		t.Fatalf("set: %v %+v", err, st)
	}
	if _, err := os.Stat(filepath.Join(dir, "config", "custom", "test.greeting.md")); err != nil {
		t.Fatalf("override file: %v", err)
	}
	if got := Text("test.greeting"); got != "Hi there" {
		t.Fatalf("override not used: %q", got)
	}
	it, _ := Lookup("test.greeting")
	if m := Missing(it, "Hi there"); len(m) != 1 || m[0] != "name" {
		t.Fatalf("missing placeholders: %v", m)
	}
	st, err = Reset("test.greeting")
	if err != nil || st.Customized || Text("test.greeting") != "Hello {{name}}." {
		t.Fatalf("reset: %v %+v", err, st)
	}
	// Saving the default text again is the same as a reset.
	if st, _ := Set("test.greeting", "Hello {{name}}."); st.Customized {
		t.Fatal("default text kept as an override")
	}
	// A blank file counts as no override.
	os.MkdirAll(filepath.Join(dir, "config", "custom"), 0o755)
	os.WriteFile(filepath.Join(dir, "config", "custom", "test.greeting.md"), []byte("  \n"), 0o644)
	if Text("test.greeting") != "Hello {{name}}." {
		t.Fatal("blank override used")
	}
}

func TestSetChecksInput(t *testing.T) {
	withData(t)
	Register(Item{ID: "test.json", Group: "Settings files", Title: "J", Kind: KindJSON, File: "j.json",
		Default: func() string { return "{}" },
		Validate: func(s string) error {
			if !strings.HasPrefix(strings.TrimSpace(s), "{") {
				return errors.New("must be a JSON object")
			}
			return nil
		}})
	if _, err := Set("test.json", "[1]"); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("validation: %v", err)
	}
	if _, err := Set("test.json", "   "); err == nil {
		t.Fatal("blank text accepted")
	}
	if _, err := Set("test.json", strings.Repeat("x", MaxBytes+1)); err == nil {
		t.Fatal("oversized text accepted")
	}
	if _, err := Set("nope", "x"); err == nil {
		t.Fatal("unknown id accepted")
	}
	st, err := Set("test.json", `{"a":1}`)
	if err != nil || !strings.HasSuffix(st.Path, filepath.Join("config", "j.json")) {
		t.Fatalf("file item path: %v %+v", err, st)
	}
}

func TestItemsAreGroupedInEditorOrder(t *testing.T) {
	Register(
		Item{ID: "test.z", Group: "Subagents", Title: "Z", Default: func() string { return "z" }},
		Item{ID: "test.a", Group: "System prompt", Title: "A", Default: func() string { return "a" }},
	)
	var groups []string
	for _, it := range Items() {
		if strings.HasPrefix(it.ID, "test.") {
			groups = append(groups, it.Group)
		}
	}
	first := map[string]int{}
	for i, g := range groups {
		if _, ok := first[g]; !ok {
			first[g] = i
		}
	}
	if first["System prompt"] > first["Subagents"] {
		t.Fatalf("group order: %v", groups)
	}
}
