package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// A server binary the workspace ships starts only when trusted; without
// that, one installed elsewhere would be used (none here).
func TestServersTheProjectShipsNeedTrust(t *testing.T) {
	ws := t.TempDir()
	bin := filepath.Join(ws, "node_modules", ".bin", "paladin-test-ls")
	writeFile(t, bin, "#!/bin/sh\n")
	os.Chmod(bin, 0o755)
	var asked []string
	no := func(tool string) bool { asked = append(asked, tool); return false }
	if got, ok := resolveBinary("paladin-test-ls", filepath.Join(ws, "src"), ws, no); ok {
		t.Fatalf("untrusted project binary picked: %s", got)
	}
	if len(asked) != 1 || asked[0] != "node_modules/.bin/paladin-test-ls" {
		t.Fatalf("asked %v", asked)
	}
	if got, ok := resolveBinary("paladin-test-ls", filepath.Join(ws, "src"), ws, func(string) bool { return true }); !ok || got != bin {
		t.Fatalf("trusted: %s %v", got, ok)
	}
	if got, ok := resolveBinary(bin, ws, ws, no); ok {
		t.Fatalf("an absolute path inside the workspace needs trust too: %s", got)
	}
	if got, ok := resolveBinary("paladin-test-ls", ws, ws, nil); !ok || got != bin {
		t.Fatalf("no gate trusts: %s %v", got, ok)
	}
}

func TestServersThatRunProjectCode(t *testing.T) {
	root := t.TempDir()
	if runsProjectCode(Spec{ID: "rust-analyzer"}, root) == "" {
		t.Fatal("rust-analyzer builds the project's build scripts")
	}
	if runsProjectCode(Spec{ID: "typescript"}, root) != "" || runsProjectCode(Spec{ID: "gopls"}, root) != "" {
		t.Fatal("no project code to run")
	}
	os.MkdirAll(filepath.Join(root, "node_modules", "typescript"), 0o755)
	if runsProjectCode(Spec{ID: "typescript"}, root) == "" {
		t.Fatal("the project's own TypeScript")
	}
}
