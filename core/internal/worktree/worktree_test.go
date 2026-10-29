package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "T")
	git(t, dir, "config", "commit.gpgsign", "false")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "s.txt"), []byte("s\n"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return "<missing>"
	}
	return strings.TrimSpace(string(b))
}

func TestCreateStartsFromCleanHeadOnANewBranch(t *testing.T) {
	repo := newRepo(t)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("dirty\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("u\n"), 0o644)
	m := New(t.TempDir())
	ctx := context.Background()
	info, cwd, err := m.Create(ctx, filepath.Join(repo, "sub"), "Fix the Bug!")
	if err != nil {
		t.Fatal(err)
	}
	if info.Branch != "paladin/fix-the-bug" || !strings.HasPrefix(info.Path, m.Root()) {
		t.Fatalf("info %+v", info)
	}
	if cwd != filepath.Join(info.Path, "sub") {
		t.Fatalf("cwd %q", cwd)
	}
	if read(t, filepath.Join(info.Path, "a.txt")) != "base" || read(t, filepath.Join(info.Path, "untracked.txt")) != "<missing>" {
		t.Fatal("worktree did not start from clean HEAD")
	}
	if info.BaseBranch != "main" || info.Base == "" {
		t.Fatalf("base %+v", info)
	}
	if read(t, filepath.Join(repo, "a.txt")) != "dirty" {
		t.Fatal("user checkout touched")
	}
	if got := git(t, info.Path, "branch", "--show-current"); got != "paladin/fix-the-bug" {
		t.Fatalf("branch %q", got)
	}
}

func TestCreateMakesNamesUniqueAndRejectsNonRepos(t *testing.T) {
	repo := newRepo(t)
	m := New(t.TempDir())
	ctx := context.Background()
	a, _, err := m.Create(ctx, repo, "task")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := m.Create(ctx, repo, "task")
	if err != nil {
		t.Fatal(err)
	}
	if a.Branch == b.Branch || a.Path == b.Path {
		t.Fatalf("not unique: %+v %+v", a, b)
	}
	c, _, err := m.Create(ctx, repo, "")
	if err != nil || !strings.HasPrefix(c.Branch, "paladin/") || len(c.Branch) <= len("paladin/") {
		t.Fatalf("default name %+v %v", c, err)
	}
	if _, _, err := m.Create(ctx, t.TempDir(), "x"); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("non repo: %v", err)
	}
	empty := t.TempDir()
	git(t, empty, "init", "-q")
	if _, _, err := m.Create(ctx, empty, "x"); err == nil {
		t.Fatal("repo without commits accepted")
	}
}

func TestStatusMergeAndNothingToMerge(t *testing.T) {
	repo := newRepo(t)
	m := New(t.TempDir())
	ctx := context.Background()
	info, _, err := m.Create(ctx, repo, "feat")
	if err != nil {
		t.Fatal(err)
	}
	if res, err := Merge(ctx, info, "nothing"); err != nil || !res.NothingToMerge {
		t.Fatalf("empty merge %+v %v", res, err)
	}
	os.WriteFile(filepath.Join(info.Path, "new.txt"), []byte("feature\n"), 0o644)
	os.WriteFile(filepath.Join(info.Path, "a.txt"), []byte("changed\n"), 0o644)
	st, err := GetStatus(ctx, info)
	if err != nil || !st.Exists || st.Changed != 2 || st.Ahead != 0 {
		t.Fatalf("status %+v %v", st, err)
	}
	res, err := Merge(ctx, info, "Add feature")
	if err != nil || !res.Merged || len(res.Conflicts) != 0 {
		t.Fatalf("merge %+v %v", res, err)
	}
	if read(t, filepath.Join(repo, "new.txt")) != "feature" || read(t, filepath.Join(repo, "a.txt")) != "changed" {
		t.Fatal("changes not merged into the repo")
	}
	if subj := git(t, repo, "log", "-1", "--format=%s"); !strings.Contains(subj, "paladin/feat") {
		t.Fatalf("merge commit subject %q", subj)
	}
	st, _ = GetStatus(ctx, info)
	if st.Changed != 0 || st.Ahead != 0 {
		t.Fatalf("status after merge %+v", st)
	}
}

func TestMergeConflictIsAbortedAndReported(t *testing.T) {
	repo := newRepo(t)
	m := New(t.TempDir())
	ctx := context.Background()
	info, _, err := m.Create(ctx, repo, "clash")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main side\n"), 0o644)
	git(t, repo, "commit", "-q", "-am", "main change")
	os.WriteFile(filepath.Join(info.Path, "a.txt"), []byte("worktree side\n"), 0o644)
	res, err := Merge(ctx, info, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if res.Merged || strings.Join(res.Conflicts, ",") != "a.txt" {
		t.Fatalf("merge %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatal("merge left in progress")
	}
	if read(t, filepath.Join(repo, "a.txt")) != "main side" || git(t, repo, "status", "--porcelain") != "" {
		t.Fatal("repo not restored after abort")
	}
	st, _ := GetStatus(ctx, info)
	if st.Changed != 0 || st.Ahead != 1 {
		t.Fatalf("worktree work should be committed on its branch: %+v", st)
	}
}

func TestMergeRefusesWhenTheRepoCheckoutIsDirty(t *testing.T) {
	repo := newRepo(t)
	m := New(t.TempDir())
	ctx := context.Background()
	info, _, _ := m.Create(ctx, repo, "dirty")
	os.WriteFile(filepath.Join(info.Path, "a.txt"), []byte("wt\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("uncommitted\n"), 0o644)
	if _, err := Merge(ctx, info, "x"); !errors.Is(err, ErrRepoDirty) {
		t.Fatalf("err %v", err)
	}
	if read(t, filepath.Join(repo, "a.txt")) != "uncommitted" {
		t.Fatal("user's uncommitted work touched")
	}
}

func TestRemoveRefusesDirtyAndKeepsUnmergedBranches(t *testing.T) {
	repo := newRepo(t)
	m := New(t.TempDir())
	ctx := context.Background()
	info, _, _ := m.Create(ctx, repo, "rm")
	os.WriteFile(filepath.Join(info.Path, "x.txt"), []byte("x\n"), 0o644)
	if err := Remove(ctx, info, false); !errors.Is(err, ErrDirty) {
		t.Fatalf("dirty remove: %v", err)
	}
	git(t, info.Path, "add", "-A")
	git(t, info.Path, "commit", "-q", "-m", "wip")
	if err := Remove(ctx, info, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
		t.Fatal("worktree dir left behind")
	}
	if got := git(t, repo, "branch", "--list", info.Branch); got == "" {
		t.Fatal("unmerged branch deleted")
	}

	merged, _, _ := m.Create(ctx, repo, "merged")
	if err := Remove(ctx, merged, false); err != nil {
		t.Fatal(err)
	}
	if got := git(t, repo, "branch", "--list", merged.Branch); got != "" {
		t.Fatalf("merged branch kept: %q", got)
	}
	if st, _ := GetStatus(ctx, merged); st.Exists {
		t.Fatal("status still sees removed worktree")
	}
}
