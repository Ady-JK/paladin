package snapshot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(dir, rel string) bool {
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func setup(t *testing.T) (*Repo, string) {
	t.Helper()
	needGit(t)
	work := t.TempDir()
	s := NewStore(t.TempDir())
	r, err := s.Repo(work)
	if err != nil {
		t.Fatal(err)
	}
	return r, work
}

func snap(t *testing.T, r *Repo) string {
	t.Helper()
	tree, err := r.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) < 40 {
		t.Fatalf("tree = %q", tree)
	}
	return tree
}

func changed(t *testing.T, r *Repo, a, b string) []string {
	t.Helper()
	got, err := r.Changed(context.Background(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	return got
}

func TestSnapshotTracksCreateModifyDeleteInAPlainFolder(t *testing.T) {
	r, work := setup(t)
	write(t, work, "a.txt", "one\n")
	write(t, work, "dir/b.txt", "bee\n")
	write(t, work, "gone.txt", "bye\n")
	before := snap(t, r)

	write(t, work, "a.txt", "two\n")
	write(t, work, "dir/new file ü.txt", "new\n")
	os.Remove(filepath.Join(work, "gone.txt"))
	after := snap(t, r)

	got := changed(t, r, before, after)
	want := []string{"a.txt", "dir/new file ü.txt", "gone.txt"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("changed = %q, want %q", got, want)
	}
	if again := snap(t, r); again != after {
		t.Fatalf("unchanged folder gave a new tree: %s vs %s", again, after)
	}
}

func TestRestoreBringsBackContentAndDeletesCreatedFiles(t *testing.T) {
	r, work := setup(t)
	write(t, work, "a.txt", "one\n")
	write(t, work, "gone.txt", "bye\n")
	before := snap(t, r)
	write(t, work, "a.txt", "two\n")
	write(t, work, "deep/x/new.txt", "new\n")
	os.Remove(filepath.Join(work, "gone.txt"))
	after := snap(t, r)

	if err := r.Restore(context.Background(), before, changed(t, r, before, after)); err != nil {
		t.Fatal(err)
	}
	if got := read(t, work, "a.txt"); got != "one\n" {
		t.Fatalf("a.txt = %q", got)
	}
	if got := read(t, work, "gone.txt"); got != "bye\n" {
		t.Fatalf("gone.txt = %q", got)
	}
	if exists(work, "deep/x/new.txt") || exists(work, "deep") {
		t.Fatal("created file (and its now-empty dirs) should be removed")
	}
}

func TestEntriesCompareStateAcrossTrees(t *testing.T) {
	r, work := setup(t)
	write(t, work, "a.txt", "one\n")
	t1 := snap(t, r)
	write(t, work, "a.txt", "two\n")
	t2 := snap(t, r)
	e1, err := r.Entries(context.Background(), t1, []string{"a.txt", "missing.txt"})
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := r.Entries(context.Background(), t2, []string{"a.txt", "missing.txt"})
	if e1["a.txt"] == "" || e1["a.txt"] == e2["a.txt"] {
		t.Fatalf("entries should differ: %q vs %q", e1["a.txt"], e2["a.txt"])
	}
	if e1["missing.txt"] != "" || e2["missing.txt"] != "" {
		t.Fatal("absent paths map to empty")
	}
}

func TestSkipsLargeBinaryAndDefaultExcludedFiles(t *testing.T) {
	r, work := setup(t)
	r.limit = 64
	write(t, work, "small.txt", "ok\n")
	write(t, work, "big.txt", strings.Repeat("x", 200))
	write(t, work, "blob.dat", "ab\x00cd")
	write(t, work, "node_modules/pkg/index.js", "x")
	write(t, work, ".venv/lib/site.py", "x")
	write(t, work, "__pycache__/m.pyc", "x")
	write(t, work, "app.exe", "MZ")
	write(t, work, ".DS_Store", "x")
	tree := snap(t, r)
	files, err := r.Files(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, "|") != "small.txt" {
		t.Fatalf("snapshot files = %q, want only small.txt", files)
	}
}

func TestAFileThatGrowsPastTheLimitDropsOutAndCanBeRestored(t *testing.T) {
	r, work := setup(t)
	r.limit = 64
	write(t, work, "f.txt", "small\n")
	before := snap(t, r)
	write(t, work, "f.txt", strings.Repeat("y", 500))
	after := snap(t, r)
	if got := changed(t, r, before, after); strings.Join(got, "|") != "f.txt" {
		t.Fatalf("changed = %q", got)
	}
	if err := r.Restore(context.Background(), before, []string{"f.txt"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, work, "f.txt"); got != "small\n" {
		t.Fatalf("f.txt = %q", got)
	}
}

// A file left out for size or content must read as "present but not
// tracked", never as absent: restoring absence would delete user data.
func TestAnUntrackedFileIsMarkedAndNeverDeletedOrOverwrittenByRestore(t *testing.T) {
	r, work := setup(t)
	r.limit = 64
	ctx := context.Background()
	write(t, work, "big.txt", strings.Repeat("x", 200))
	write(t, work, "blob.dat", "ab\x00cd")
	before := snap(t, r)
	write(t, work, "big.txt", "now small\n")
	write(t, work, "blob.dat", "text now\n")
	after := snap(t, r)
	got := changed(t, r, before, after)
	if strings.Join(got, "|") != "big.txt|blob.dat" {
		t.Fatalf("changed = %q", got)
	}
	entries, err := r.Entries(ctx, before, got)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if entries[p] == "" || !r.Untracked(entries[p]) {
			t.Fatalf("%s before = %q, want an untracked marker", p, entries[p])
		}
	}
	if err := r.Restore(ctx, before, got); err != nil {
		t.Fatal(err)
	}
	if read(t, work, "big.txt") != "now small\n" || read(t, work, "blob.dat") != "text now\n" {
		t.Fatal("restore touched a file whose original was never captured")
	}
}

func TestRestoreRefusesPathsBehindASymlinkedFolder(t *testing.T) {
	r, work := setup(t)
	ctx := context.Background()
	outside := t.TempDir()
	write(t, outside, "f.txt", "precious\n")
	empty := snap(t, r)
	write(t, work, "sub/f.txt", "agent\n")
	snap(t, r)
	if err := os.RemoveAll(filepath.Join(work, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "sub")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := r.Restore(ctx, empty, []string{"sub/f.txt"}); err == nil {
		t.Fatal("restore through a symlinked folder succeeded")
	}
	if read(t, outside, "f.txt") != "precious\n" {
		t.Fatal("restore deleted a file outside the folder")
	}
}

func TestRespectsTheProjectGitignoreAndLeavesTheUserRepoAlone(t *testing.T) {
	r, work := setup(t)
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	write(t, work, ".gitignore", "secret.env\nout/\n")
	write(t, work, "main.go", "package main\n")
	git("add", ".")
	git("commit", "-qm", "init")
	head := git("rev-parse", "HEAD")
	write(t, work, "secret.env", "TOKEN=1")
	write(t, work, "out/bin.txt", "x")
	write(t, work, "main.go", "package main // edited\n")
	write(t, work, "untracked.txt", "u")

	tree := snap(t, r)
	files, _ := r.Files(context.Background(), tree)
	if strings.Join(files, "|") != ".gitignore|main.go|untracked.txt" {
		t.Fatalf("files = %q", files)
	}
	if git("rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved")
	}
	if staged := git("diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("user index changed: %q", staged)
	}
	if st := git("status", "--porcelain"); !strings.Contains(st, "?? untracked.txt") {
		t.Fatalf("status = %q", st)
	}
}

func TestRefusesHomeAndFilesystemRoot(t *testing.T) {
	needGit(t)
	s := NewStore(t.TempDir())
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/", home} {
		if dir == "" {
			continue
		}
		if _, err := s.Repo(dir); !errors.Is(err, ErrRefused) {
			t.Fatalf("Repo(%q) err = %v, want ErrRefused", dir, err)
		}
	}
}

func TestTooManyFilesDisablesTheFolder(t *testing.T) {
	r, work := setup(t)
	r.maxFiles = 3
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		write(t, work, n+".txt", n)
	}
	if _, err := r.Snapshot(context.Background()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if _, err := r.Snapshot(context.Background()); !errors.Is(err, ErrTooLarge) {
		t.Fatal("folder should stay disabled")
	}
}

func TestSameFolderSharesOneRepoAndConcurrentSnapshotsAreSafe(t *testing.T) {
	needGit(t)
	work := t.TempDir()
	s := NewStore(t.TempDir())
	a, _ := s.Repo(work)
	b, _ := s.Repo(work + string(filepath.Separator))
	if a != b {
		t.Fatal("same folder should share a repo")
	}
	write(t, work, "f.txt", "x")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Snapshot(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestRestoreRejectsPathsOutsideTheFolder(t *testing.T) {
	r, _ := setup(t)
	tree := snap(t, r)
	if err := r.Restore(context.Background(), tree, []string{"../escape.txt"}); err == nil {
		t.Fatal("expected an error for a path outside the folder")
	}
}

func TestPathspecMagicIsTakenLiterally(t *testing.T) {
	r, work := setup(t)
	write(t, work, "a*.txt", "star\n")
	write(t, work, "ab.txt", "ab\n")
	before := snap(t, r)
	write(t, work, "a*.txt", "changed\n")
	write(t, work, "ab.txt", "changed\n")
	if err := r.Restore(context.Background(), before, []string{"a*.txt"}); err != nil {
		t.Fatal(err)
	}
	if read(t, work, "a*.txt") != "star\n" || read(t, work, "ab.txt") != "changed\n" {
		t.Fatal("restore must only touch the literal path")
	}
}
