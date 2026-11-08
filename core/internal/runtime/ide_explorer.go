package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type IDEExplorerSettings struct {
	HideIgnored    bool `json:"hideIgnored"`
	HideDotFolders bool `json:"hideDotFolders"`
}

const maxIDEExplorerSettingsBytes = 64 << 10

func explorerConfigPath(cwd string, create bool) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("IDE workspace must be an absolute path")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory")
	}
	dir := cwd
	for _, name := range []string{".config", "paladin"} {
		dir = filepath.Join(dir, name)
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return filepath.Join(dir, "explorer.json"), os.ErrNotExist
			}
			if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(dir)
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("workspace config path is not a directory: %s", dir)
		}
	}
	path := filepath.Join(dir, "explorer.json")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("explorer config is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func IDEExplorerSettingsGet(cwd string) (IDEExplorerSettings, error) {
	var settings IDEExplorerSettings
	path, err := explorerConfigPath(cwd, false)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIDEExplorerSettingsBytes+1))
	if err != nil {
		return settings, err
	}
	if len(data) > maxIDEExplorerSettingsBytes {
		return settings, fmt.Errorf("explorer settings exceed 64 KiB")
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return settings, fmt.Errorf("invalid explorer settings: %w", err)
	}
	return settings, nil
}
func IDEExplorerSettingsSet(cwd string, settings IDEExplorerSettings) error {
	path, err := explorerConfigPath(cwd, true)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'))
}

func ignoredIDEEntries(root, dir string, entries []os.DirEntry) (map[string]bool, error) {
	var input bytes.Buffer
	for _, entry := range entries {
		input.WriteString(filepath.Join(dir, entry.Name()))
		input.WriteByte(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Stdin = &input
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("checking ignored files: %w", ctx.Err())
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return map[string]bool{}, nil
		}
		// Non-repositories and systems without Git still honor workspace ignore rules.
		return tools.IDEIgnoreFallback(root, dir, entries)
	}
	ignored := make(map[string]bool)
	for _, path := range bytes.Split(output, []byte{0}) {
		if len(path) > 0 {
			ignored[filepath.Base(string(path))] = true
		}
	}
	return ignored, nil
}
func (r *AgentRuntime) IDEList(cwd, path string, settings IDEExplorerSettings) (FsList, error) {
	if !filepath.IsAbs(cwd) {
		return FsList{}, fmt.Errorf("IDE workspace must be an absolute path")
	}
	_, scope, err := tools.ResolveScopedPath("IDE", "path", path, cwd)
	if err != nil {
		return FsList{}, err
	}
	if scope != tools.ScopeInside {
		return FsList{}, fmt.Errorf("folder is outside the workspace")
	}
	abs, entries, err := r.readFsDirectory(path)
	if err != nil {
		return FsList{}, err
	}
	ignored := map[string]bool{}
	if settings.HideIgnored {
		ignored, err = ignoredIDEEntries(cwd, abs, entries)
		if err != nil {
			return FsList{}, err
		}
	}
	return formatFsList(abs, entries, func(entry os.DirEntry) bool {
		return !ignored[entry.Name()] && !(settings.HideDotFolders && entry.IsDir() && strings.HasPrefix(entry.Name(), "."))
	}), nil
}
