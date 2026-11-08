package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIDEFileRPC(t *testing.T) {
	host := startHost(t, buildHost(t), t.TempDir())
	cwd := t.TempDir()
	path := filepath.Join(cwd, "hello.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	read := host.call("read", "ideReadFile", map[string]any{"cwd": cwd, "path": path})
	if read["ok"] != true {
		t.Fatalf("read: %v", read)
	}
	file := read["result"].(map[string]any)
	missing := host.call("missing", "ideSaveFile", map[string]any{"cwd": cwd, "path": path, "version": file["version"]})
	if missing["ok"] == true {
		t.Fatalf("save without content succeeded: %v", missing)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "package main\n" {
		t.Fatalf("missing content changed file: %q %v", data, err)
	}
	save := host.call("save", "ideSaveFile", map[string]any{"cwd": cwd, "path": path, "content": "package app\n", "version": file["version"]})
	if save["ok"] != true {
		t.Fatalf("save: %v", save)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "package app\n" {
		t.Fatalf("written file: %q %v", data, err)
	}
	conflict := host.call("conflict", "ideSaveFile", map[string]any{"cwd": cwd, "path": path, "content": "stale\n", "version": file["version"]})
	if conflict["ok"] == true {
		t.Fatalf("stale save succeeded: %v", conflict)
	}
}
