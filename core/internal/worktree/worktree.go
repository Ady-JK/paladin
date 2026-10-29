// Package worktree manages per-session git worktrees: an isolated
// checkout on its own branch, under the app's data dir, that can be
// merged back into the repository it came from.
package worktree

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var (
	// ErrNotRepo means the folder is not inside a git repository.
	ErrNotRepo = errors.New("not a git repository")
	// ErrDirty means the worktree has uncommitted changes.
	ErrDirty = errors.New("worktree has uncommitted changes")
	// ErrRepoDirty means the main checkout has uncommitted changes to
	// tracked files, so a merge could clobber them.
	ErrRepoDirty = errors.New("the repository checkout has uncommitted changes; commit or stash them first")
)

// BranchPrefix namespaces branches created for sessions.
const BranchPrefix = "paladin/"

// Info identifies one managed worktree.
type Info struct {
	RepoRoot   string `json:"repoRoot"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Base       string `json:"base"`
	BaseBranch string `json:"baseBranch,omitempty"`
}

// Status is a worktree's state relative to its repository.
type Status struct {
	Exists  bool `json:"exists"`
	Changed int  `json:"changed"`
	Ahead   int  `json:"ahead"`
}

// MergeResult reports a merge back into the repository.
type MergeResult struct {
	Merged         bool     `json:"merged"`
	NothingToMerge bool     `json:"nothingToMerge"`
	Commit         string   `json:"commit,omitempty"`
	Conflicts      []string `json:"conflicts"`
	Into           string   `json:"into,omitempty"`
}

// Manager creates worktrees under one root folder.
type Manager struct {
	root string
	mu   sync.Mutex
}

func New(root string) *Manager { return &Manager{root: root} }

// Root is the folder holding all managed worktrees.
func (m *Manager) Root() string { return m.root }

func gitEnv() []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.quotePath=false"}, args...)...)
	cmd.Env = gitEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.String(), fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return out.String(), nil
}

func trimmed(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := run(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// RepoRoot returns the top level of the repository containing dir.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNotRepo, dir)
	}
	root, err := trimmed(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return "", fmt.Errorf("%w: %s", ErrNotRepo, dir)
	}
	return filepath.Clean(root), nil
}

// Slug turns a free-form name into a branch-safe slug.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

func randomSlug() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "session-" + hex.EncodeToString(b[:])
}

func repoFolderName(root string) string {
	sum := sha256.Sum256([]byte(root))
	base := Slug(filepath.Base(root))
	if base == "" {
		base = "repo"
	}
	return base + "-" + hex.EncodeToString(sum[:3])
}

// Create adds a worktree for the repository containing dir, on a new
// branch from its current HEAD commit (uncommitted changes stay behind).
// It returns the worktree and the folder matching dir inside it.
func (m *Manager) Create(ctx context.Context, dir, name string) (Info, string, error) {
	root, err := RepoRoot(ctx, dir)
	if err != nil {
		return Info{}, "", err
	}
	head, err := trimmed(ctx, root, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil || head == "" {
		return Info{}, "", fmt.Errorf("the repository has no commits yet; make a first commit to use worktrees")
	}
	baseBranch, _ := trimmed(ctx, root, "branch", "--show-current")
	slug := Slug(name)
	if slug == "" {
		slug = randomSlug()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	parent := filepath.Join(m.root, repoFolderName(root))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Info{}, "", err
	}
	var branch, path string
	for i := 1; ; i++ {
		cand := slug
		if i > 1 {
			cand = slug + "-" + strconv.Itoa(i)
		}
		branch, path = BranchPrefix+cand, filepath.Join(parent, cand)
		_, statErr := os.Lstat(path)
		_, refErr := trimmed(ctx, root, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
		if os.IsNotExist(statErr) && refErr != nil {
			break
		}
		if i > 200 {
			return Info{}, "", fmt.Errorf("no free worktree name for %q", slug)
		}
	}
	if _, err := run(ctx, root, "worktree", "add", "-q", "-b", branch, path, head); err != nil {
		return Info{}, "", err
	}
	info := Info{RepoRoot: root, Path: path, Branch: branch, Base: head, BaseBranch: baseBranch}
	cwd := path
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if rel, err := filepath.Rel(root, real); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			if st, err := os.Stat(filepath.Join(path, rel)); err == nil && st.IsDir() {
				cwd = filepath.Join(path, rel)
			}
		}
	}
	return info, cwd, nil
}

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func changedCount(ctx context.Context, dir string, untracked bool) (int, error) {
	args := []string{"status", "--porcelain", "-z"}
	if !untracked {
		args = append(args, "--untracked-files=no")
	}
	out, err := run(ctx, dir, args...)
	if err != nil {
		return 0, err
	}
	n := 0
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 3 {
			continue
		}
		n++
		if f[0] == 'R' || f[0] == 'C' {
			i++ // the rename source follows as its own field
		}
	}
	return n, nil
}

func ahead(ctx context.Context, info Info) (int, error) {
	out, err := trimmed(ctx, info.RepoRoot, "rev-list", "--count", "HEAD.."+info.Branch)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// GetStatus reports whether the worktree exists, how many files it has
// changed and how many commits its branch holds that HEAD lacks.
func GetStatus(ctx context.Context, info Info) (Status, error) {
	if !exists(info.Path) {
		return Status{}, nil
	}
	st := Status{Exists: true}
	var err error
	if st.Changed, err = changedCount(ctx, info.Path, true); err != nil {
		return st, err
	}
	if st.Ahead, err = ahead(ctx, info); err != nil {
		return st, err
	}
	return st, nil
}

// identity falls back to a Paladin identity only when the user has none.
func identity(ctx context.Context, dir string) []string {
	if email, _ := trimmed(ctx, dir, "config", "user.email"); email != "" {
		return nil
	}
	return []string{"-c", "user.name=Paladin", "-c", "user.email=paladin@localhost"}
}

// Merge commits any work in the worktree onto its branch and merges the
// branch into the repository's current checkout with a merge commit.
// Conflicts abort the merge, leaving the repository as it was.
func Merge(ctx context.Context, info Info, message string) (MergeResult, error) {
	res := MergeResult{Conflicts: []string{}}
	if !exists(info.Path) {
		return res, fmt.Errorf("worktree %s no longer exists", info.Path)
	}
	if n, err := changedCount(ctx, info.RepoRoot, false); err != nil {
		return res, err
	} else if n > 0 {
		return res, ErrRepoDirty
	}
	if n, err := changedCount(ctx, info.Path, true); err != nil {
		return res, err
	} else if n > 0 {
		if _, err := run(ctx, info.Path, "add", "-A"); err != nil {
			return res, err
		}
		msg := strings.TrimSpace(message)
		if msg == "" {
			msg = "Work from " + info.Branch
		}
		args := append(identity(ctx, info.Path), "commit", "-q", "-m", msg)
		if _, err := run(ctx, info.Path, args...); err != nil {
			return res, err
		}
	}
	res.Into, _ = trimmed(ctx, info.RepoRoot, "branch", "--show-current")
	n, err := ahead(ctx, info)
	if err != nil {
		return res, err
	}
	if n == 0 {
		res.NothingToMerge = true
		return res, nil
	}
	args := append(identity(ctx, info.RepoRoot), "merge", "--no-ff", "--no-edit", info.Branch)
	if _, err := run(ctx, info.RepoRoot, args...); err != nil {
		conflicts, _ := trimmed(ctx, info.RepoRoot, "diff", "--name-only", "--diff-filter=U")
		_, _ = run(ctx, info.RepoRoot, "merge", "--abort")
		if conflicts == "" {
			return res, err
		}
		res.Conflicts = strings.Split(conflicts, "\n")
		return res, nil
	}
	res.Merged = true
	res.Commit, _ = trimmed(ctx, info.RepoRoot, "rev-parse", "HEAD")
	return res, nil
}

// Remove deletes the worktree (refusing uncommitted work unless force)
// and its branch when that branch is fully merged.
func Remove(ctx context.Context, info Info, force bool) error {
	if exists(info.Path) {
		if !force {
			if n, err := changedCount(ctx, info.Path, true); err != nil {
				return err
			} else if n > 0 {
				return ErrDirty
			}
		}
		args := []string{"worktree", "remove"}
		if force {
			args = append(args, "--force")
		}
		if _, err := run(ctx, info.RepoRoot, append(args, info.Path)...); err != nil {
			return err
		}
	} else {
		_, _ = run(ctx, info.RepoRoot, "worktree", "prune")
	}
	// Unmerged work stays reachable on its branch.
	_, _ = run(ctx, info.RepoRoot, "branch", "-d", info.Branch)
	_ = os.Remove(filepath.Dir(info.Path))
	return nil
}

// Head returns the current branch (empty when detached) and HEAD commit
// (empty before the first commit) of the repository at root.
func Head(ctx context.Context, root string) (branch, head string) {
	branch, _ = trimmed(ctx, root, "branch", "--show-current")
	head, _ = trimmed(ctx, root, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	return branch, head
}

// CommonDir is the git dir shared by all of a repository's worktrees.
func CommonDir(ctx context.Context, dir string) string {
	out, err := trimmed(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return out
}
