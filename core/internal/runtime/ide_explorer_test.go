package runtime

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIDEExplorerSettingsAndFiltering(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".hidden", "ignored", "src"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, contents := range map[string]string{".gitignore": "ignored/\n*.log\n!important.log\n", "debug.log": "", "important.log": "", "src/.gitignore": "secret.txt\n", "src/secret.txt": "", "src/visible.txt": ""} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	defaults, err := IDEExplorerSettingsGet(root)
	if err != nil || defaults.HideIgnored || defaults.HideDotFolders {
		t.Fatalf("defaults: %+v %v", defaults, err)
	}
	settings := IDEExplorerSettings{HideIgnored: true, HideDotFolders: true}
	if err := IDEExplorerSettingsSet(root, settings); err != nil {
		t.Fatal(err)
	}
	got, err := IDEExplorerSettingsGet(root)
	if err != nil || got != settings {
		t.Fatalf("settings: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".config", "paladin", "explorer.json")); err != nil {
		t.Fatal(err)
	}
	rt := &AgentRuntime{}
	list, err := rt.IDEList(root, root, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list.Directories {
		if d.Name == ".hidden" || d.Name == "ignored" {
			t.Fatalf("hidden directory present: %+v", d)
		}
	}
	for _, f := range list.Files {
		if f.Name == "debug.log" {
			t.Fatalf("ignored file present: %+v", f)
		}
	}
	found := false
	for _, f := range list.Files {
		if f.Name == "important.log" {
			found = true
		}
	}
	if !found {
		t.Fatal("negated file missing")
	}
	child, err := rt.IDEList(root, filepath.Join(root, "src"), settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range child.Files {
		if f.Name == "secret.txt" {
			t.Fatal("nested ignore not applied")
		}
	}
	other, err := IDEExplorerSettingsGet(t.TempDir())
	if err != nil || other.HideIgnored || other.HideDotFolders {
		t.Fatalf("settings leaked: %+v %v", other, err)
	}
	if _, err := rt.IDEList(root, filepath.Dir(root), settings); err == nil {
		t.Fatal("allowed listing outside workspace")
	}
}

func TestIDEExplorerUsesGitIgnoreRules(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	for name, data := range map[string]string{".gitignore": "*.log\n!important.log\nfile[0-9].txt\n", "debug.log": "", "important.log": "", "file1.txt": "", "filex.txt": ""} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	list, err := (&AgentRuntime{}).IDEList(root, root, IDEExplorerSettings{HideIgnored: true})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range list.Files {
		names[file.Name] = true
	}
	if names["debug.log"] || names["file1.txt"] || !names["important.log"] || !names["filex.txt"] {
		t.Fatalf("incorrect Git ignore matching: %v", names)
	}
}

func TestIDEExplorerRejectsSymlinkConfig(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := IDEExplorerSettingsSet(root, IDEExplorerSettings{HideIgnored: true}); err == nil {
		t.Fatal("followed config symlink")
	}
}

func TestIDEExplorerRejectsOversizedSettings(t *testing.T) {
	root := t.TempDir()
	if err := IDEExplorerSettingsSet(root, IDEExplorerSettings{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".config", "paladin", "explorer.json")
	data := append([]byte(`{"padding":"`), bytes.Repeat([]byte("x"), 64*1024)...)
	data = append(data, []byte(`"}`)...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := IDEExplorerSettingsGet(root); err == nil {
		t.Fatal("oversized settings were accepted")
	}
}

func TestIDEExplorerRejectsOversizedIgnoreFileOutsideGit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), bytes.Repeat([]byte("x"), (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&AgentRuntime{}).IDEList(root, root, IDEExplorerSettings{HideIgnored: true}); err == nil {
		t.Fatal("oversized ignore file was silently accepted")
	}
}
