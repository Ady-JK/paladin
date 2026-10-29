package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "s.txt"), []byte("s\n"), 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	return dir
}

func wtRuntime(t *testing.T, rounds ...string) *AgentRuntime {
	t.Helper()
	rt, _ := testRuntime(t)
	_ = rt.store.Keys.Set("openrouter", "sk-test")
	pointMockAt(rt, scriptModel(t, rounds...).URL)
	return rt
}

func TestOpenSessionInAWorktree(t *testing.T) {
	repo := gitRepo(t)
	rt := wtRuntime(t)
	sess, err := rt.OpenSessionWith("", filepath.Join(repo, "sub"), OpenOpts{Worktree: true, Name: "Feat"})
	if err != nil {
		t.Fatal(err)
	}
	wt := sess.Worktree
	if wt == nil || wt.Branch != "paladin/feat" || !strings.HasPrefix(sess.Cwd, filepath.Join(rt.dataDir, "worktrees")) || filepath.Base(sess.Cwd) != "sub" {
		t.Fatalf("session %+v wt %+v", sess, wt)
	}
	if wt.Origin != filepath.Join(repo, "sub") || wt.BaseBranch != "main" {
		t.Fatalf("worktree %+v", wt)
	}
	infos, _ := rt.ListSessions("", 0)
	if len(infos) != 1 || infos[0].Worktree == nil || infos[0].Worktree.Branch != "paladin/feat" {
		t.Fatalf("list %+v", infos)
	}
	if again, _ := rt.OpenSession(sess.SessionID, ""); again.Worktree == nil {
		t.Fatal("reopen lost the worktree")
	}
	if _, err := rt.OpenSessionWith("", t.TempDir(), OpenOpts{Worktree: true}); err == nil {
		t.Fatal("worktree outside a repo accepted")
	}
	gi := rt.GitInfo(repo)
	if !gi.Repo || gi.Branch != "main" || !gi.HasCommits {
		t.Fatalf("git info %+v", gi)
	}
	if gi := rt.GitInfo(t.TempDir()); gi.Repo {
		t.Fatalf("git info outside repo %+v", gi)
	}
}

func TestForkIntoAWorktreeCopiesTheConversation(t *testing.T) {
	repo := gitRepo(t)
	rt := wtRuntime(t, "")
	src, _ := rt.OpenSession("", repo)
	turn(t, rt, src.SessionID, repo, "hello")
	fork, err := rt.ForkSessionWith(src.SessionID, "", OpenOpts{Worktree: true})
	if err != nil {
		t.Fatal(err)
	}
	if fork.Worktree == nil || fork.Cwd == repo || !strings.HasSuffix(fork.Title, "(fork)") {
		t.Fatalf("fork %+v", fork)
	}
	if rows := userRows(t, rt, fork.SessionID); len(rows) != 1 || rows[0].Content != "hello" {
		t.Fatalf("history %+v", rows)
	}
}

func TestWorktreeSessionIsIsolatedAndMergesBack(t *testing.T) {
	repo := gitRepo(t)
	rt := wtRuntime(t, "echo feature > new.txt", "")
	sess, err := rt.OpenSessionWith("", repo, OpenOpts{Worktree: true, Name: "iso"})
	if err != nil {
		t.Fatal(err)
	}
	turn(t, rt, sess.SessionID, sess.Cwd, "add a file")
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("worktree turn leaked into the repo")
	}
	st, err := rt.WorktreeStatus(sess.SessionID, "")
	if err != nil || st.Changed != 1 || st.Branch != "paladin/iso" || st.Into != "main" {
		t.Fatalf("status %+v %v", st, err)
	}
	res, err := rt.WorktreeMerge(sess.SessionID, "", "")
	if err != nil || !res.Merged {
		t.Fatalf("merge %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "new.txt")); string(b) != "feature\n" {
		t.Fatalf("merged file %q", b)
	}
	if _, err := rt.WorktreeStatus(mustOpenPlain(t, rt, repo), ""); err == nil {
		t.Fatal("status on a plain session should fail")
	}
}

func mustOpenPlain(t *testing.T, rt *AgentRuntime, cwd string) string {
	t.Helper()
	s, err := rt.OpenSession("", cwd)
	if err != nil {
		t.Fatal(err)
	}
	return s.SessionID
}

func TestDeleteCleansUpTheWorktreeWithItsLastSession(t *testing.T) {
	repo := gitRepo(t)
	rt := wtRuntime(t, "")
	a, err := rt.OpenSessionWith("", repo, OpenOpts{Worktree: true, Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.ForkSessionWith(a.SessionID, "", OpenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Cwd != a.Cwd || b.Worktree == nil {
		t.Fatalf("plain fork of a worktree session should stay in it: %+v", b)
	}
	path := a.Worktree.Path
	if _, err := rt.DeleteSessionWith(a.SessionID, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("worktree removed while another session uses it")
	}
	os.WriteFile(filepath.Join(path, "wip.txt"), []byte("wip\n"), 0o644)
	if _, err := rt.DeleteSessionWith(b.SessionID, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "wip.txt")); err != nil {
		t.Fatal("dirty worktree removed without discard")
	}

	c, _ := rt.OpenSessionWith("", repo, OpenOpts{Worktree: true, Name: "gone"})
	os.WriteFile(filepath.Join(c.Worktree.Path, "wip.txt"), []byte("wip\n"), 0o644)
	if _, err := rt.DeleteSessionWith(c.SessionID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Worktree.Path); !os.IsNotExist(err) {
		t.Fatal("discarded worktree still on disk")
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "paladin/gone").CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("empty branch kept: %s", out)
	}
}
