// Package snapshot records folder states in a shadow git repository so
// an agent's file changes can be undone. Each folder gets its own git
// dir under the store root; the folder itself is only ever the work
// tree, so the user's own repository (index, HEAD, branches) is never
// touched. Snapshots are plain tree objects: cheap, deduplicated and
// diffable.
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrUnavailable means git is not installed.
	ErrUnavailable = errors.New("snapshots need git, which was not found")
	// ErrRefused guards folders far too broad to track (/, home).
	ErrRefused = errors.New("snapshots are off for this folder (too broad: pick a project folder)")
	// ErrTooLarge disables a folder with too many trackable files.
	ErrTooLarge = errors.New("snapshots are off for this folder (too many files)")
)

const (
	// DefaultLimit skips files larger than this.
	DefaultLimit = 10 << 20
	// DefaultMaxFiles disables folders with more pending files than this.
	DefaultMaxFiles = 50000
	sniffBytes      = 8000
)

// hardExcludes keeps dependency trees, caches and compiled or media
// artifacts out of every snapshot. The folder's own ignore files apply
// on top.
var hardExcludes = []string{
	".git/", "node_modules/", "bower_components/", "jspm_packages/", ".pnpm-store/", ".yarn/cache/",
	".venv/", "venv/", "__pycache__/", ".pytest_cache/", ".mypy_cache/", ".ruff_cache/", ".tox/", ".nox/",
	"*.egg-info/", ".ipynb_checkpoints/", ".gradle/", ".terraform/", "Pods/", "DerivedData/", ".dart_tool/",
	".DS_Store", "Thumbs.db", "*.log", "*.swp",
	"*.pyc", "*.pyo", "*.class", "*.jar", "*.war", "*.o", "*.obj", "*.a", "*.lib", "*.so", "*.dylib", "*.dll", "*.exe",
	"*.zip", "*.tar", "*.gz", "*.tgz", "*.bz2", "*.xz", "*.7z", "*.rar", "*.dmg", "*.iso",
	"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.ico", "*.pdf", "*.mp3", "*.mp4", "*.mov", "*.wav",
	"*.woff", "*.woff2", "*.ttf", "*.otf", "*.sqlite", "*.db",
}

// guessExcludes are usual build-output folders. They only apply to a
// folder outside any git repository: inside one, the project's own
// .gitignore knows better (some repos keep sources in build/).
var guessExcludes = []string{
	"dist/", "build/", "out/", "target/", ".next/", ".nuxt/", ".svelte-kit/", ".turbo/", ".parcel-cache/",
	".cache/", ".serverless/", "coverage/", ".nyc_output/", ".idea/", ".vs/", "*.tmp",
}

func inGitRepo(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// Store hands out one Repo per folder.
type Store struct {
	root  string
	mu    sync.Mutex
	repos map[string]*Repo
}

// NewStore keeps shadow git dirs under root.
func NewStore(root string) *Store {
	return &Store{root: root, repos: map[string]*Repo{}}
}

// Repo returns the shared snapshot repo for cwd.
func (s *Store) Repo(cwd string) (*Repo, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrUnavailable
	}
	work, err := normalize(cwd)
	if err != nil {
		return nil, err
	}
	if tooBroad(work) {
		return nil, ErrRefused
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.repos[work]; ok {
		return r, nil
	}
	sum := sha256.Sum256([]byte(work))
	name := filepath.Base(work)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "root"
	}
	r := &Repo{
		gitDir:   filepath.Join(s.root, safeName(name)+"-"+hex.EncodeToString(sum[:8])),
		work:     work,
		limit:    DefaultLimit,
		maxFiles: DefaultMaxFiles,
		binary:   map[string]stamp{},

		exclusive: make(chan struct{}, 1),
	}
	s.repos[work] = r
	return r, nil
}

func normalize(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", fmt.Errorf("snapshot: folder is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("snapshot: %s is not a folder", abs)
	}
	return filepath.Clean(abs), nil
}

func tooBroad(dir string) bool {
	if filepath.Dir(dir) == dir {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if h, err := filepath.EvalSymlinks(home); err == nil {
			home = h
		}
		if filepath.Clean(home) == dir {
			return true
		}
	}
	return false
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

type stamp struct {
	size int64
	mod  time.Time
}

// Repo is one folder's snapshot history.
type Repo struct {
	gitDir   string
	work     string
	limit    int64
	maxFiles int

	mu       sync.Mutex
	ready    bool
	disabled error
	// binary remembers files already sniffed as binary.
	binary map[string]stamp
	// marker is the placeholder blob standing in for files snapshots
	// leave out (too large or binary), so "present but not captured"
	// never reads as absent.
	marker string
	// exclusive serialises whole snapshot → mutation → snapshot
	// brackets (and reverts) for this folder; see Exclusive.
	exclusive chan struct{}
}

// Exclusive waits for sole use of the folder's mutation bracket. Store
// shares one Repo per canonical folder, so this is a workspace-keyed
// lock: brackets in one folder run one at a time while other folders
// (including worktrees) proceed. It is in-process only; editors and
// other programs are not held back.
func (r *Repo) Exclusive(ctx context.Context) (release func(), err error) {
	select {
	case r.exclusive <- struct{}{}:
		return func() { <-r.exclusive }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// markerBody is the placeholder content of files left out of snapshots.
const markerBody = "paladin-snapshot: file present but not captured (too large or binary)\n"

// Dir is the tracked folder.
func (r *Repo) Dir() string { return r.work }

func (r *Repo) env() []string {
	env := make([]string, 0, len(os.Environ())+8)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_DIR="+r.gitDir,
		"GIT_WORK_TREE="+r.work,
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	)
}

func (r *Repo) git(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"-c", "core.autocrlf=false", "-c", "core.safecrlf=false", "-c", "core.quotePath=false",
		"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull,
	}, args...)...)
	cmd.Dir = r.work
	cmd.Env = r.env()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("snapshot: git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func (r *Repo) init(ctx context.Context) error {
	if r.ready {
		return nil
	}
	if _, err := os.Stat(filepath.Join(r.gitDir, "HEAD")); err != nil {
		if err := os.MkdirAll(r.gitDir, 0o700); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
		if _, err := r.git(ctx, nil, "init", "-q"); err != nil {
			return err
		}
		for _, kv := range [][2]string{{"gc.auto", "0"}, {"core.longpaths", "true"}, {"core.bare", "false"}} {
			if _, err := r.git(ctx, nil, "config", kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	info := filepath.Join(r.gitDir, "info")
	if err := os.MkdirAll(info, 0o700); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	excludes := hardExcludes
	if !inGitRepo(r.work) {
		excludes = append(append([]string{}, hardExcludes...), guessExcludes...)
	}
	body := "# written by paladin\n" + strings.Join(excludes, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(info, "exclude"), []byte(body), 0o600); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	marker, err := r.git(ctx, []byte(markerBody), "hash-object", "-w", "--stdin")
	if err != nil {
		return err
	}
	r.marker = strings.TrimSpace(string(marker))
	r.ready = true
	return nil
}

func splitNul(b []byte) []string {
	var out []string
	for _, p := range bytes.Split(b, []byte{0}) {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}

func joinNul(paths []string) []byte {
	var b bytes.Buffer
	for _, p := range paths {
		b.WriteString(p)
		b.WriteByte(0)
	}
	return b.Bytes()
}

// Snapshot records the folder as it is now and returns the tree id.
func (r *Repo) Snapshot(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disabled != nil {
		return "", r.disabled
	}
	if err := r.init(ctx); err != nil {
		return "", err
	}
	out, err := r.git(ctx, nil, "ls-files", "-z", "--others", "--modified", "--exclude-standard")
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var candidates []string
	for _, p := range splitNul(out) {
		// Nested repositories list as "dir/": never descend into them.
		if strings.HasSuffix(p, "/") || seen[p] {
			continue
		}
		seen[p] = true
		candidates = append(candidates, p)
	}
	if len(candidates) > r.maxFiles {
		r.disabled = ErrTooLarge
		return "", r.disabled
	}
	var add, skip []string
	for _, p := range candidates {
		if r.skippable(p) {
			skip = append(skip, p)
		} else {
			add = append(add, p)
		}
	}
	if len(skip) > 0 {
		var info bytes.Buffer
		for _, p := range skip {
			fmt.Fprintf(&info, "100644 %s\t%s\x00", r.marker, p)
		}
		if _, err := r.git(ctx, info.Bytes(), "update-index", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	if len(add) > 0 {
		if _, err := r.git(ctx, joinNul(add), "add", "-A", "--ignore-errors", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			// A file vanishing mid-add is a race with the agent, not a
			// failure: take the rest on a second pass.
			if _, err2 := r.git(ctx, joinNul(add), "add", "-A", "--ignore-errors", "--pathspec-from-file=-", "--pathspec-file-nul"); err2 != nil {
				return "", err
			}
		}
	}
	tree, err := r.git(ctx, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(tree)), nil
}

// skippable reports files snapshots leave out: too large or binary.
// Missing files are never skipped (their deletion is recorded).
func (r *Repo) skippable(rel string) bool {
	full := filepath.Join(r.work, filepath.FromSlash(rel))
	st, err := os.Lstat(full)
	if err != nil || !st.Mode().IsRegular() {
		delete(r.binary, rel)
		return false
	}
	if st.Size() > r.limit {
		return true
	}
	if s, ok := r.binary[rel]; ok && s.size == st.Size() && s.mod.Equal(st.ModTime()) {
		return true
	}
	f, err := os.Open(full)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, sniffBytes)
	n, _ := io.ReadFull(f, buf)
	if bytes.IndexByte(buf[:n], 0) >= 0 {
		r.binary[rel] = stamp{size: st.Size(), mod: st.ModTime()}
		return true
	}
	delete(r.binary, rel)
	return false
}

// Changed lists paths that differ between two snapshots.
func (r *Repo) Changed(ctx context.Context, from, to string) ([]string, error) {
	if from == to {
		return nil, nil
	}
	out, err := r.git(ctx, nil, "diff-tree", "-r", "-z", "--no-renames", "--name-only", from, to)
	if err != nil {
		return nil, err
	}
	return splitNul(out), nil
}

// DiffFile returns a unified patch for one recorded file between two
// immutable trees. It does not inspect the current worktree.
func (r *Repo) DiffFile(ctx context.Context, from, to, rel string) (string, error) {
	if err := CheckPath(rel); err != nil {
		return "", err
	}
	out, err := r.git(ctx, nil, "diff", "--no-ext-diff", "--no-renames", "--no-color", "--unified=3", from, to, "--", rel)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Files lists every captured path in a snapshot.
func (r *Repo) Files(ctx context.Context, tree string) ([]string, error) {
	out, err := r.git(ctx, nil, "ls-tree", "-r", "-z", tree)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range splitNul(out) {
		meta, path, ok := strings.Cut(line, "\t")
		if f := strings.Fields(meta); ok && len(f) == 3 && f[2] != r.marker {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

// Untracked reports an Entries value that is a placeholder for a file
// that existed but was never captured: it cannot be restored.
func (r *Repo) Untracked(entry string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.marker != "" && strings.HasSuffix(entry, " "+r.marker)
}

// safeParents rejects a path whose parent folders include a symlink, so
// a restore can never write or delete outside the folder.
func (r *Repo) safeParents(rel string) error {
	dir := r.work
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		st, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil // the restore creates the rest of the path
		}
		if err != nil {
			return fmt.Errorf("snapshot: %s: %w", rel, err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot: %s is behind a symlink", rel)
		}
	}
	return nil
}

// Entries maps each path to "mode hash" in tree, or "" when absent.
func (r *Repo) Entries(ctx context.Context, tree string, paths []string) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	want := map[string]bool{}
	for _, p := range paths {
		out[p] = ""
		want[p] = true
	}
	for i := 0; i < len(paths); i += 200 {
		end := min(i+200, len(paths))
		args := append([]string{"ls-tree", "-r", "-z", "--full-tree", tree, "--"}, paths[i:end]...)
		res, err := r.git(ctx, nil, args...)
		if err != nil {
			return nil, err
		}
		for _, line := range splitNul(res) {
			meta, path, ok := strings.Cut(line, "\t")
			if !ok || !want[path] {
				continue
			}
			f := strings.Fields(meta)
			if len(f) == 3 {
				out[path] = f[0] + " " + f[2]
			}
		}
	}
	return out, nil
}

// CheckPath rejects paths that would leave the folder.
func CheckPath(rel string) error {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return fmt.Errorf("snapshot: bad path %q", rel)
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || part == "." || part == "" || strings.EqualFold(part, ".git") {
			return fmt.Errorf("snapshot: bad path %q", rel)
		}
	}
	return nil
}

// Restore writes each path back to its state in tree: present files are
// checked out, files absent from tree are deleted (with any parent
// folders that end up empty).
func (r *Repo) Restore(ctx context.Context, tree string, paths []string) error {
	for _, p := range paths {
		if err := CheckPath(p); err != nil {
			return err
		}
	}
	if len(paths) == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.init(ctx); err != nil {
		return err
	}
	entries, err := r.Entries(ctx, tree, paths)
	if err != nil {
		return err
	}
	var present, absent []string
	for _, p := range paths {
		if err := r.safeParents(p); err != nil {
			return err
		}
		switch e := entries[p]; {
		case e != "" && strings.HasSuffix(e, " "+r.marker):
			// Never captured: leave whatever is there alone.
		case e != "":
			present = append(present, p)
		default:
			absent = append(absent, p)
		}
	}
	if len(present) > 0 {
		for _, p := range present {
			// A folder now sitting where the file was would block checkout.
			full := filepath.Join(r.work, filepath.FromSlash(p))
			if st, err := os.Lstat(full); err == nil && st.IsDir() {
				if err := os.RemoveAll(full); err != nil {
					return fmt.Errorf("snapshot: %w", err)
				}
			}
		}
		if _, err := r.git(ctx, joinNul(present), "checkout", tree, "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return err
		}
	}
	for _, p := range absent {
		full := filepath.Join(r.work, filepath.FromSlash(p))
		if st, err := os.Lstat(full); err == nil && !st.IsDir() {
			if err := os.Remove(full); err != nil {
				return fmt.Errorf("snapshot: %w", err)
			}
		}
		r.pruneEmpty(filepath.Dir(full))
	}
	return nil
}

func (r *Repo) pruneEmpty(dir string) {
	for dir != r.work && strings.HasPrefix(dir, r.work+string(filepath.Separator)) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// Supported reports whether this platform can take snapshots at all.
func Supported() bool {
	_, err := exec.LookPath("git")
	return err == nil && runtime.GOOS != "js"
}
