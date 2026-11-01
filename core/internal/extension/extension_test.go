package extension

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

func TestInitNeverOverwritesUserFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "extensions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"enabled":["mine.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"enabled":["mine.ts"]}` {
		t.Fatalf("manifest changed: %s, %v", data, err)
	}
}

func TestDisabledExtensionDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	moduleDir, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "executed")
	code := `import {writeFileSync} from 'node:fs'; writeFileSync(` + quoted(marker) + `, 'yes'); export default {tools:[]};`
	if err := os.WriteFile(filepath.Join(moduleDir, "probe.ts"), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	got, failures := Load(context.Background(), dir)
	if len(got) != 0 || len(failures) != 0 {
		t.Fatalf("disabled module loaded: %v %v", got, failures)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("disabled module executed")
	}
}

func TestEnabledTypeScriptTool(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is not installed")
	}
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "config", "extensions.json")
	if err := os.WriteFile(manifest, []byte(`{"enabled":["hello.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, failures := Load(context.Background(), dir)
	if len(failures) != 0 {
		t.Fatal(failures)
	}
	if len(got) != 1 || got[0].Name() != "extension__hello__hello" {
		t.Fatalf("bad tools: %v", got)
	}
	output, err := got[0].Call(json.RawMessage(`{"name":"Ada"}`), &tools.ToolCtx{Workspace: dir, Cancel: context.Background()})
	if err != nil || !strings.Contains(output, `"Hello, Ada"`) {
		t.Fatalf("call: %s %v", output, err)
	}
}

func TestManifestRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "config", "extensions.json")
	if err := os.WriteFile(manifest, []byte(`{"enabled":["../../escape.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, failures := Load(context.Background(), dir)
	if len(got) != 0 || len(failures) != 1 {
		t.Fatalf("path escape accepted: %v %v", got, failures)
	}
}
