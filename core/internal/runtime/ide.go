package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

const ideMaxFileBytes = 2 << 20

type IDEFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Version string `json:"version"`
}

func ideFilePath(cwd, path string) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("IDE workspace must be an absolute path")
	}
	resolved, scope, err := tools.ResolveScopedPath("IDE", "path", path, cwd)
	if err != nil {
		return "", err
	}
	if scope != tools.ScopeInside {
		return "", fmt.Errorf("file is outside the workspace")
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("only regular files can be edited")
	}
	return resolved, nil
}

func IDEReadFile(cwd, path string) (IDEFile, error) {
	resolved, err := ideFilePath(cwd, path)
	if err != nil {
		return IDEFile{}, err
	}
	f, err := os.Open(resolved)
	if err != nil {
		return IDEFile{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, ideMaxFileBytes+1))
	if err != nil {
		return IDEFile{}, err
	}
	if len(data) > ideMaxFileBytes {
		return IDEFile{}, fmt.Errorf("file exceeds the 2 MiB editor limit")
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return IDEFile{}, fmt.Errorf("file is not UTF-8 text")
	}
	sum := sha256.Sum256(data)
	return IDEFile{Path: resolved, Content: string(data), Version: hex.EncodeToString(sum[:])}, nil
}

func IDESaveFile(cwd, path, content, version string) (IDEFile, error) {
	if len(content) > ideMaxFileBytes || !utf8.ValidString(content) || bytes.IndexByte([]byte(content), 0) >= 0 {
		return IDEFile{}, fmt.Errorf("content must be UTF-8 text no larger than 2 MiB")
	}
	current, err := IDEReadFile(cwd, path)
	if err != nil {
		return IDEFile{}, err
	}
	if version == "" || current.Version != version {
		return IDEFile{}, fmt.Errorf("file changed on disk; reopen it before saving")
	}
	info, err := os.Stat(current.Path)
	if err != nil {
		return IDEFile{}, err
	}
	if err := atomicfile.WriteMode(current.Path, []byte(content), info.Mode()); err != nil {
		return IDEFile{}, err
	}
	sum := sha256.Sum256([]byte(content))
	return IDEFile{Path: current.Path, Content: content, Version: hex.EncodeToString(sum[:])}, nil
}
