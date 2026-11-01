// servers.go knows which language server handles a file: the built-in
// specs below, adjusted by the user's <data>/config/lsp.json. A server
// is used only when its binary is already installed, either in the
// project (node_modules/.bin, a virtualenv) or on PATH.

package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Spec describes one language server.
type Spec struct {
	ID string `json:"-"`
	// Command is the argv; Command[0] is looked up per project.
	Command []string `json:"command"`
	// Extensions are the file extensions it handles, e.g. ".go".
	Extensions []string `json:"extensions"`
	// LanguageID is sent in didOpen; "" derives it from the extension.
	LanguageID string `json:"languageId,omitempty"`
	// RootMarkers pick the project root: the nearest folder holding one.
	RootMarkers []string `json:"rootMarkers,omitempty"`
	// Env adds variables to the server's environment.
	Env      []string `json:"env,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

// builtinSpecs are the servers Paladin knows out of the box.
func builtinSpecs() map[string]Spec {
	return map[string]Spec{
		"gopls": {Command: []string{"gopls"}, Extensions: []string{".go"}, LanguageID: "go",
			RootMarkers: []string{"go.work", "go.mod"}},
		"typescript": {Command: []string{"typescript-language-server", "--stdio"},
			Extensions:  []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
			RootMarkers: []string{"tsconfig.json", "jsconfig.json", "package.json"}},
		"pyright": {Command: []string{"pyright-langserver", "--stdio"}, Extensions: []string{".py", ".pyi"},
			LanguageID: "python", RootMarkers: []string{"pyrightconfig.json", "pyproject.toml", "setup.py", "setup.cfg"}},
		"basedpyright": {Command: []string{"basedpyright-langserver", "--stdio"}, Extensions: []string{".py", ".pyi"},
			LanguageID: "python", RootMarkers: []string{"pyrightconfig.json", "pyproject.toml", "setup.py", "setup.cfg"}},
		"rust-analyzer": {Command: []string{"rust-analyzer"}, Extensions: []string{".rs"}, LanguageID: "rust",
			RootMarkers: []string{"Cargo.toml"}},
		"clangd": {Command: []string{"clangd"}, Extensions: []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh"},
			RootMarkers: []string{"compile_commands.json", ".clangd", "CMakeLists.txt"}},
	}
}

// ConfigPath is the user's server overrides.
func ConfigPath(dataDir string) string { return filepath.Join(dataDir, "config", "lsp.json") }

type configFile struct {
	// TimeoutMs is how long write/edit wait for diagnostics.
	TimeoutMs int `json:"timeoutMs,omitempty"`
	// Servers override built-ins by id (fields given replace theirs) or
	// add new ones.
	Servers map[string]Spec `json:"servers,omitempty"`
}

// loadSpecs merges lsp.json over the built-ins.
func loadSpecs(dataDir string) (map[string]Spec, int, error) {
	specs := builtinSpecs()
	data, err := os.ReadFile(ConfigPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return withIDs(specs), 0, nil
	}
	if err != nil {
		return withIDs(specs), 0, err
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return withIDs(specs), 0, err
	}
	for id, o := range cfg.Servers {
		s := specs[id]
		if len(o.Command) > 0 {
			s.Command = o.Command
		}
		if len(o.Extensions) > 0 {
			s.Extensions = o.Extensions
		}
		if o.LanguageID != "" {
			s.LanguageID = o.LanguageID
		}
		if len(o.RootMarkers) > 0 {
			s.RootMarkers = o.RootMarkers
		}
		if len(o.Env) > 0 {
			s.Env = o.Env
		}
		s.Disabled = o.Disabled
		specs[id] = s
	}
	return withIDs(specs), cfg.TimeoutMs, nil
}

func withIDs(specs map[string]Spec) map[string]Spec {
	for id, s := range specs {
		s.ID = id
		specs[id] = s
	}
	return specs
}

// candidates are the enabled specs for path's extension, in id order
// (so pyright is tried before basedpyright).
func candidates(specs map[string]Spec, path string) []Spec {
	ext := strings.ToLower(filepath.Ext(path))
	var out []Spec
	for _, s := range specs {
		if s.Disabled || len(s.Command) == 0 {
			continue
		}
		for _, e := range s.Extensions {
			if strings.EqualFold(e, ext) {
				out = append(out, s)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// resolveBinary finds argv[0] for a project: an absolute path as is, a
// project-local install (node_modules/.bin, .venv/bin) between the file
// and the workspace, else PATH.
func resolveBinary(name, dir, workspace string, trust func(string) bool) (string, bool) {
	if filepath.IsAbs(name) {
		if st, err := os.Stat(name); err == nil && st.Mode().IsRegular() {
			if tools.Within(name, workspace) && trust != nil && !trust(tools.ProjectLabel(workspace, name)) {
				return "", false
			}
			return name, true
		}
		return "", false
	}
	for d := filepath.Clean(dir); tools.Within(d, workspace); d = filepath.Dir(d) {
		for _, rel := range []string{"node_modules/.bin", ".venv/bin", "venv/bin"} {
			bin := filepath.Join(d, filepath.FromSlash(rel), name)
			if st, err := os.Stat(bin); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
				// A server the project ships runs only if trusted;
				// otherwise one installed elsewhere is used.
				if trust != nil && !trust(tools.ProjectLabel(workspace, bin)) {
					d = filepath.Clean(workspace)
					break
				}
				return bin, true
			}
		}
		if d == filepath.Clean(workspace) || filepath.Dir(d) == d {
			break
		}
	}
	bin, err := tools.LookCommand(name)
	return bin, err == nil
}

// projectRoot is the nearest folder from dir up to the workspace that
// holds one of markers, else the workspace.
func projectRoot(dir, workspace string, markers []string) string {
	ws := filepath.Clean(workspace)
	for d := filepath.Clean(dir); tools.Within(d, ws); d = filepath.Dir(d) {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return d
			}
		}
		if d == ws || filepath.Dir(d) == d {
			break
		}
	}
	return ws
}

// languageID maps an extension to an LSP language id.
func languageID(s Spec, path string) string {
	if s.LanguageID != "" {
		return s.LanguageID
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return "cpp"
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// ValidateConfig checks lsp.json text before it is saved.
func ValidateConfig(text string) error {
	var cfg configFile
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("lsp.json: %w", err)
	}
	for id, s := range cfg.Servers {
		if s.Disabled {
			continue
		}
		if _, builtin := builtinSpecs()[id]; !builtin && (len(s.Command) == 0 || len(s.Extensions) == 0) {
			return fmt.Errorf("lsp.json: server %q needs a command and extensions", id)
		}
	}
	return nil
}
