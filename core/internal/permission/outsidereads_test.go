package permission

import (
	"os"
	"path/filepath"
	"testing"
)

func outsideFixture(t *testing.T) (ws, outside, secret string) {
	t.Helper()
	ws = t.TempDir()
	outside = t.TempDir()
	secret = filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	return ws, outside, secret
}

func reportsRead(reads []readTarget, want string) bool {
	for _, r := range reads {
		if r.path == want || r.real == want {
			return true
		}
	}
	return false
}

// A path glued to a short option (-f/path) is still a path the command reads.
func TestOutsideReadsSeesAPathAttachedToAnOption(t *testing.T) {
	ws, _, secret := outsideFixture(t)
	reads, _ := outsideReads([]string{"grep", "-f" + secret, "pattern", "x"}, ws)
	if !reportsRead(reads, secret) {
		t.Fatalf("reads = %+v, want %s", reads, secret)
	}
}

// A glob can expand through a symlink to a file outside the workspace.
func TestOutsideReadsExpandsGlobsThroughSymlinks(t *testing.T) {
	ws, _, secret := outsideFixture(t)
	reads, _ := outsideReads([]string{"cat", "l*/secret.txt"}, ws)
	if !reportsRead(reads, secret) && !reportsRead(reads, filepath.Join(ws, "link", "secret.txt")) {
		t.Fatalf("reads = %+v, want the secret behind the link", reads)
	}
}

// Search patterns are not paths: they must not turn into prompts.
func TestOutsideReadsLeavesPatternsAlone(t *testing.T) {
	ws, _, _ := outsideFixture(t)
	for _, words := range [][]string{
		{"grep", "-rn", "foo.*bar", "."},
		{"rg", "-e", "a|b", "src"},
		{"grep", "-E", "x[0-9]+", "file.txt"},
	} {
		if reads, variable := outsideReads(words, ws); len(reads) != 0 || variable {
			t.Errorf("%v: reads=%+v variable=%v", words, reads, variable)
		}
	}
}

func TestProtectedDirCoversFilesOtherToolsExecute(t *testing.T) {
	ws := t.TempDir()
	for _, rel := range []string{
		".mcp.json", ".envrc", ".vscode/tasks.json", ".husky/pre-commit",
		".gitattributes", ".gitmodules", ".cargo/config.toml", ".npmrc", "sub/.husky/pre-push",
	} {
		if got := protectedDir(ws, filepath.Join(ws, rel)); got == "" {
			t.Errorf("%s is not protected", rel)
		}
	}
	for _, rel := range []string{"README.md", "src/main.go", ".gitignore", "docs/.github-notes.md", "package.json", ".github/workflows/ci.yml"} {
		if got := protectedDir(ws, filepath.Join(ws, rel)); got != "" {
			t.Errorf("%s is protected: %s", rel, got)
		}
	}
}
