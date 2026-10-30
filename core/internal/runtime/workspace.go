// The workspace folder, the folder browser and @-file completion.

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/syserr"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

type FsList struct {
	CurrentPath string        `json:"currentPath"`
	Directories []FsDirectory `json:"directories"`
	ParentPath  string        `json:"parentPath"`
	Files       []FsFile      `json:"files"`
}

type FsDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FsFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (r *AgentRuntime) GetWorkspace() string {
	return r.store.Workspace()
}

func (r *AgentRuntime) SetWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	cleaned := ""
	if path != "" {
		var abs string
		if filepath.IsAbs(path) {
			abs = path
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("workspace %q is not reachable: %s", path, syserr.Str(err))
			}
			abs = filepath.Join(cwd, path)
		}
		abs = cleanAbs(abs)
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("workspace %q is not reachable: %s", path, syserr.Str(err))
		}
		if !info.IsDir() {
			return "", fmt.Errorf("workspace %q is not a directory", path)
		}
		cleaned = abs
	}
	r.providersMu.Lock()
	defer r.providersMu.Unlock()
	file, err := r.store.LoadProviders()
	if err != nil {
		return "", err
	}
	if len(file.Providers) == 0 {
		file = config.EnsureSeeded(config.DefaultProvidersFile())
	}
	file.Workspace = cleaned
	if err := r.store.SaveProviders(file); err != nil {
		return "", err
	}
	return cleaned, nil
}

func cleanAbs(path string) string {
	// Resolve . and .. lexically, without touching the filesystem.
	vol := filepath.VolumeName(path)
	rest := path[len(vol):]
	isAbs := strings.HasPrefix(rest, "/")
	parts := strings.Split(rest, "/")
	var out []string
	for _, p := range parts {
		switch p {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, p)
		}
	}
	joined := strings.Join(out, "/")
	if isAbs {
		joined = "/" + joined
	}
	if result := vol + joined; result != "" {
		return result
	}
	return "/"
}

func (r *AgentRuntime) FsHome() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

func (r *AgentRuntime) FsList(path string) (FsList, error) {
	path = strings.TrimSpace(path)
	target := path
	if target == "" {
		target = r.FsHome()
	}
	var abs string
	if filepath.IsAbs(target) {
		abs = target
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return FsList{}, fmt.Errorf("path %q is not valid: %w", target, err)
		}
		abs = filepath.Join(cwd, target)
	}
	abs = cleanAbs(abs)
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return FsList{}, fmt.Errorf("path %q does not exist", abs)
		}
		if os.IsPermission(err) {
			return FsList{}, fmt.Errorf("path %q cannot be read: permission denied", abs)
		}
		return FsList{}, fmt.Errorf("path %q cannot be listed: %s", abs, syserr.Str(err))
	}
	directories := []FsDirectory{}
	files := []FsFile{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			switch strings.ToLower(name) {
			case "system volume information", "$recycle.bin", "recovery":
				continue
			}
			directories = append(directories, FsDirectory{Name: name, Path: filepath.Join(abs, name)})
			continue
		}
		files = append(files, FsFile{Name: name, Path: filepath.Join(abs, name)})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].Name < directories[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	parent := filepath.Dir(abs)
	if parent == abs {
		// The root has no parent.
		parent = ""
	}
	return FsList{CurrentPath: abs, ParentPath: parent, Directories: directories, Files: files}, nil
}

// FileEntry is one `@` completion row: Path is the Linux-style chat
// path (`src/foo.ts`, `../sib/file`, `src/sub/` for folders),
// ResolvedPath is the OS-specific absolute path the composer inserts
// into the message, Outside flags workspace escapes.
type FileEntry struct {
	Path         string `json:"path"`
	Display      string `json:"display"`
	ResolvedPath string `json:"resolvedPath"`
	Outside      bool   `json:"outside"`
	IsDir        bool   `json:"isDir"`
}

// FileComplete suggests workspace files and folders for a partial
// chat path. Gitignored paths are never suggested. Queries starting
// with `..` complete from the workspace parent so outside files and
// folders tag correctly.
func (r *AgentRuntime) FileComplete(query, cwd string, limit int) ([]FileEntry, error) {
	entries, err := tools.CompleteFiles(resolveCwd(r, cwd), query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, FileEntry{Path: e.Path, Display: e.Display, ResolvedPath: e.ResolvedPath, Outside: e.Outside, IsDir: e.IsDir})
	}
	return out, nil
}

// resolveCwd falls back to the runtime workspace when the caller
// passes no cwd.
func resolveCwd(r *AgentRuntime, cwd string) string {
	if strings.TrimSpace(cwd) != "" {
		return cwd
	}
	return r.GetWorkspace()
}
