package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// IDESearchHit identifies one matching line inside the workspace.
type IDESearchHit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type IDESearchResult struct {
	Hits      []IDESearchHit `json:"hits"`
	Truncated bool           `json:"truncated"`
}

// IDESearch searches literal text using the same ignore and size limits as grep.
func IDESearch(root, query string) (IDESearchResult, error) {
	result := IDESearchResult{Hits: []IDESearchHit{}}
	if err := validateIDESearch(root, query); err != nil {
		return result, err
	}
	re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(query))
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	files, engine, truncated, err := listFiles(ctx, root)
	if err != nil {
		return result, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	const limit = 200
	ignored := make(map[string]map[string]bool)
	for _, file := range files {
		if ctx.Err() != nil {
			truncated = true
			break
		}
		if engine == "builtin" {
			skip, err := ideSearchIgnored(root, file.rel, ignored)
			if err != nil {
				return result, err
			}
			if skip {
				continue
			}
		}
		hits, _, more := grepFileHits(file.path, re, 0, limit-len(result.Hits))
		for _, hit := range hits {
			result.Hits = append(result.Hits, IDESearchHit{Path: file.path, Line: hit.line, Text: hit.text})
		}
		if more || len(result.Hits) == limit {
			truncated = true
			break
		}
	}
	result.Truncated = truncated
	return result, nil
}

func validateIDESearch(root, query string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("IDE workspace must be an absolute path")
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("IDE workspace is not a directory")
	}
	if strings.TrimSpace(query) == "" || len(query) > 512 {
		return fmt.Errorf("search text must be between 1 and 512 bytes")
	}
	return nil
}

// The builtin walker also runs outside Git; check each ancestor so an
// ignored directory cannot expose its contents in search results.
func ideSearchIgnored(root, rel string, cache map[string]map[string]bool) (bool, error) {
	dir := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		entries, ok := cache[dir]
		if !ok {
			listing, err := os.ReadDir(dir)
			if err != nil {
				return false, err
			}
			entries, err = IDEIgnoreFallback(root, dir, listing)
			if err != nil {
				return false, err
			}
			cache[dir] = entries
		}
		if entries[part] {
			return true, nil
		}
		dir = filepath.Join(dir, part)
	}
	return false, nil
}
