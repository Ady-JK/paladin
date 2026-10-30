package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

// File is an mcp.json document.
type File struct {
	McpServers map[string]ServerConfig `json:"mcpServers"`
}

// GlobalPath is the app-wide server list.
func GlobalPath(dataDir string) string { return filepath.Join(dataDir, "config", "mcp.json") }

// FolderPath is a folder's own server list.
func FolderPath(folder string) string { return filepath.Join(folder, ".paladin", "mcp.json") }

// ParseFile parses and validates an mcp.json document. Blank input is
// an empty list.
func ParseFile(data []byte) (File, error) {
	var f File
	if strings.TrimSpace(string(data)) == "" {
		return File{McpServers: map[string]ServerConfig{}}, nil
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if f.McpServers == nil {
		f.McpServers = map[string]ServerConfig{}
	}
	for name, cfg := range f.McpServers {
		if err := validateServer(name, cfg); err != nil {
			return File{}, err
		}
	}
	return f, nil
}

// readFile loads path; a missing file is an empty list.
func readFile(path string) (File, []byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{McpServers: map[string]ServerConfig{}}, nil, nil
	}
	if err != nil {
		return File{}, nil, err
	}
	f, err := ParseFile(data)
	return f, data, err
}

// WriteFile writes an mcp.json atomically, creating parent folders.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data = []byte(strings.TrimRight(string(data), "\n") + "\n")
	return atomicfile.Write(path, data)
}

var varRef = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandString replaces ${VAR}, ${VAR:-default} and ${workspaceFolder};
// a bare $ is left alone so arguments like regexes survive.
func expandString(s, folder string) string {
	return varRef.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		def := ""
		if i := strings.Index(name, ":-"); i >= 0 {
			name, def = name[:i], name[i+2:]
		}
		if name == "workspaceFolder" && folder != "" {
			return folder
		}
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v
		}
		return def
	})
}

// expand resolves variables in a copy of cfg. Folder servers run in
// their folder unless they say otherwise; relative cwds resolve there.
func expand(cfg ServerConfig, folder string) ServerConfig {
	out := cfg
	out.Command = expandString(cfg.Command, folder)
	out.URL = expandString(cfg.URL, folder)
	out.Cwd = expandString(cfg.Cwd, folder)
	out.Args = make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		out.Args[i] = expandString(a, folder)
	}
	out.Env = maps.Clone(cfg.Env)
	for k, v := range out.Env {
		out.Env[k] = expandString(v, folder)
	}
	out.Headers = maps.Clone(cfg.Headers)
	for k, v := range out.Headers {
		out.Headers[k] = expandString(v, folder)
	}
	switch {
	case out.Cwd == "" && folder != "":
		out.Cwd = folder
	case out.Cwd != "" && !filepath.IsAbs(out.Cwd) && folder != "":
		out.Cwd = filepath.Join(folder, out.Cwd)
	}
	return out
}

// Fingerprint identifies one version of a folder's mcp.json.
func Fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// trustStore remembers which folder configs the user approved. Trust
// is pinned to the file's content: any edit (a pulled commit, say)
// must be approved again before its commands run.
type trustStore struct {
	path string
	mu   sync.Mutex
}

type trustFile struct {
	Folders map[string]string `json:"folders"`
}

func newTrustStore(path string) *trustStore { return &trustStore{path: path} }

func (t *trustStore) load() trustFile {
	var f trustFile
	if data, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(data, &f)
	}
	if f.Folders == nil {
		f.Folders = map[string]string{}
	}
	return f
}

// Trusted reports whether folder's config with fingerprint fp is approved.
func (t *trustStore) Trusted(folder, fp string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	got, ok := t.load().Folders[folder]
	return ok && got == fp
}

// Set approves fp for folder; "" revokes.
func (t *trustStore) Set(folder, fp string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.load()
	if fp == "" {
		delete(f.Folders, folder)
	} else {
		f.Folders[folder] = fp
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(t.path, data)
}
