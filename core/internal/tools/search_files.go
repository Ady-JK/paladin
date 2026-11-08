package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// fileEntry is one enumerated regular file.
type fileEntry struct {
	path    string
	rel     string
	modTime time.Time
}

// listFiles enumerates the regular files under root, honoring
// .gitignore where it can. truncated reports the searchMaxFiles cut.
func listFiles(ctx context.Context, root string) (files []fileEntry, engine string, truncated bool, err error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, "", false, err
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, "builtin", false, nil
		}
		return []fileEntry{{path: root, rel: filepath.Base(root), modTime: info.ModTime()}}, "builtin", false, nil
	}
	if rg, ok := ripgrep(); ok {
		rels, cut, err := runLister(ctx, root, rg, "--files", "--hidden", "--no-follow", "--no-messages", "--no-require-git",
			"--glob", "!.git", "--null", ".")
		if err == nil {
			return statFiles(root, rels), "rg", cut, nil
		}
	}
	if rels, cut, err := gitFiles(ctx, root); err == nil {
		return statFiles(root, rels), "builtin", cut, nil
	}
	files, truncated, err = walkSearchFiles(ctx, root)
	return files, "builtin", truncated, err
}

// gitFiles lists tracked and untracked, non-ignored files under root
// when root is inside a git work tree.
func gitFiles(ctx context.Context, root string) ([]string, bool, error) {
	git, err := LookCommand("git")
	if err != nil {
		return nil, false, err
	}
	// Searches run without approval: a repository's fsmonitor command
	// (config, not files) must not run with them.
	safe := []string{"-C", root, "-c", "core.fsmonitor=false"}
	// ls-files itself fails outside a work tree, so a separate rev-parse
	// probe would only start another process for every search.
	return runLister(ctx, root, git, append(safe, "ls-files", "--cached", "--others", "--exclude-standard", "-z")...)
}

// runLister runs a NUL-separated file lister in root and collects at
// most searchMaxFiles relative paths.
func runLister(ctx context.Context, root, bin string, args ...string) ([]string, bool, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = root
	cmd.Env = ChildEnv()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	var rels []string
	truncated := false
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 64*1024), 1<<20)
	scan.Split(splitNUL)
	for scan.Scan() {
		if len(rels) == searchMaxFiles {
			truncated = true
			break
		}
		rel := filepath.FromSlash(strings.TrimPrefix(scan.Text(), "./"))
		if rel != "" && !skippedPath(rel) {
			rels = append(rels, rel)
		}
	}
	scanErr := scan.Err()
	if truncated || scanErr != nil {
		_ = cmd.Process.Kill()
	}
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()
	if scanErr != nil {
		return nil, false, fmt.Errorf("read file listing: %w", scanErr)
	}
	if truncated {
		err = nil
	}
	// rg exits 1 when it lists nothing; that is an empty tree.
	var exit *exec.ExitError
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(bin)), ".exe")
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(rels) == 0 && name != "git" {
		err = nil
	}
	return rels, truncated, err
}

func splitNUL(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// skippedPath reports whether rel runs through a skipped folder; git
// can list a checked-in node_modules, which is still noise.
func skippedPath(rel string) bool {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if searchSkipDirs[part] {
			return true
		}
	}
	return false
}

// statFiles keeps the regular files among rels; symlinks and vanished
// paths drop out.
func statFiles(root string, rels []string) []fileEntry {
	out := make([]fileEntry, 0, len(rels))
	for _, rel := range rels {
		path := filepath.Join(root, rel)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, fileEntry{path: path, rel: rel, modTime: info.ModTime()})
	}
	return out
}

func walkSearchFiles(ctx context.Context, root string) ([]fileEntry, bool, error) {
	var out []fileEntry
	errFull := errors.New("full")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root && searchSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(out) == searchMaxFiles {
			return errFull
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil //nolint:nilerr // a file that vanished mid-walk is skipped
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, fileEntry{path: path, rel: rel, modTime: info.ModTime()})
		return nil
	})
	if errors.Is(err, errFull) {
		return out, true, nil
	}
	return out, false, err
}
