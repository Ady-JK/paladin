package tools

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// IDEIgnoreFallback reads only the ancestor .gitignore files for one directory.
// It is used outside Git repositories, where git check-ignore is unavailable.
func IDEIgnoreFallback(root, dir string, entries []os.DirEntry) (map[string]bool, error) {
	m := &ignoreMatcher{root: cleanPath(root)}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return nil, err
	}
	current := root
	parts := []string{""}
	if rel != "." {
		parts = append(parts, strings.Split(rel, string(filepath.Separator))...)
	}
	for _, part := range parts {
		if part != "" {
			current = filepath.Join(current, part)
		}
		if err := appendIDEIgnoreRules(m, current); err != nil {
			return nil, err
		}
	}
	ignored := make(map[string]bool)
	for _, entry := range entries {
		if m.ignored(filepath.Join(dir, entry.Name()), entry.IsDir()) {
			ignored[entry.Name()] = true
		}
	}
	return ignored, nil
}

// appendIDEIgnoreRules reads one regular ignore file, subject to the size limit.
func appendIDEIgnoreRules(matcher *ignoreMatcher, directory string) error {
	ignorePath := filepath.Join(directory, ".gitignore")
	info, err := os.Lstat(ignorePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	file, err := os.Open(ignorePath)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	closeErr := file.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("ignore file %q exceeds 1 MiB", ignorePath)
	}
	for _, line := range strings.Split(string(data), "\n") {
		pattern, dirOnly, anchored, neg, ok := parseIgnoreLine(line)
		if ok {
			matcher.rules = append(matcher.rules, ignoreRule{dir: cleanPath(directory), pattern: pattern, dirOnly: dirOnly, anchored: anchored, neg: neg})
		}
	}
	return nil
}
