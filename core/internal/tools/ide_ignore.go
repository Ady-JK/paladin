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
		ignorePath := filepath.Join(current, ".gitignore")
		info, err := os.Lstat(ignorePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(ignorePath)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) > 1<<20 {
			return nil, fmt.Errorf("ignore file %q exceeds 1 MiB", ignorePath)
		}
		for _, line := range strings.Split(string(data), "\n") {
			pattern, dirOnly, anchored, neg, ok := parseIgnoreLine(line)
			if ok {
				m.rules = append(m.rules, ignoreRule{dir: cleanPath(current), pattern: pattern, dirOnly: dirOnly, anchored: anchored, neg: neg})
			}
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
