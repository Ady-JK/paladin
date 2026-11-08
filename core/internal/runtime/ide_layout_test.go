package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIDELayoutIsPrivateAndWorkspaceScoped(t *testing.T) {
	rt, _ := testRuntime(t)
	first, second := t.TempDir(), t.TempDir()
	data := json.RawMessage(`{"activePath":"hello.go","draft":"private draft"}`)
	if err := rt.IDELayoutSet(first, data); err != nil {
		t.Fatal(err)
	}
	got, err := rt.IDELayoutGet(first)
	if err != nil || string(got) != string(data) {
		t.Fatalf("layout=%s error=%v", got, err)
	}
	other, err := rt.IDELayoutGet(second)
	if err != nil || string(other) != "null" {
		t.Fatalf("other=%s error=%v", other, err)
	}
	entries, err := os.ReadDir(filepath.Join(rt.dataDir, "ide-layout"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
}

func TestIDELayoutRejectsInvalidData(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.IDELayoutSet("relative", json.RawMessage(`{}`)); err == nil {
		t.Fatal("relative workspace accepted")
	}
	if err := rt.IDELayoutSet(t.TempDir(), json.RawMessage(`invalid`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
