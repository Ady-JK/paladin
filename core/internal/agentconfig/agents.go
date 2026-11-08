// Package agentconfig reads user-defined subagent definitions.
package agentconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/config"
)

const FileName = "agent.json"
const maxBytes = 128 << 10

var validName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)

type Definition struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Permissions string              `json:"permissions"`
	Tools       map[string]bool     `json:"tools"`
	MCP         map[string][]string `json:"mcp"`
	Role        string              `json:"role"`
}

// Dir isolates managed and test data directories, while the normal desktop
// installation uses the user's requested ~/.config/paladin/agents location.
func Dir(dataDir string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("PALADIN_AGENTS_DIR")); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("PALADIN_AGENTS_DIR must be absolute")
		}
		return filepath.Clean(override), nil
	}
	if strings.TrimSpace(os.Getenv("PALADIN_DATA_DIR")) != "" || filepath.Clean(dataDir) != filepath.Clean(config.DataDir()) {
		return filepath.Join(dataDir, "agents"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "paladin", "agents"), nil
}

func List(root string) ([]Definition, []error) {
	// ReadDir sorts by folder name; validated agent names match their folders.
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	var out []Definition
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || !validName.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(root, entry.Name(), FileName)
		data, err := readAgentFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
			continue
		}
		var d Definition
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&d); err == nil {
			var extra any
			if e := dec.Decode(&extra); e != io.EOF {
				if e == nil {
					err = fmt.Errorf("unexpected additional JSON value")
				} else {
					err = e
				}
			}
		}
		if err == nil {
			err = validate(entry.Name(), d)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
			continue
		}
		out = append(out, d)
	}
	return out, failures
}

// readAgentFile checks the opened file as well as the directory entry, so a
// replacement during discovery cannot redirect the read or bypass the cap.
func readAgentFile(path string) ([]byte, error) {
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() || entry.Size() > maxBytes {
		return nil, fmt.Errorf("invalid or oversized agent file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		return nil, fmt.Errorf("agent file changed during discovery")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("invalid or oversized agent file")
	}
	return data, nil
}

func validate(folder string, d Definition) error {
	if d.Name != folder || !validName.MatchString(d.Name) {
		return fmt.Errorf("name must match the folder and use lowercase letters, digits, - or _")
	}
	if strings.TrimSpace(d.Description) == "" || len(d.Description) > 500 {
		return fmt.Errorf("description must be 1–500 bytes")
	}
	if d.Permissions != "read-only" && d.Permissions != "read-write" {
		return fmt.Errorf("permissions must be read-only or read-write")
	}
	if strings.TrimSpace(d.Role) == "" || len(d.Role) > 64<<10 {
		return fmt.Errorf("role must be 1–65536 bytes")
	}
	if d.Tools == nil || d.MCP == nil {
		return fmt.Errorf("tools and mcp objects are required")
	}
	for name := range d.Tools {
		if strings.TrimSpace(name) == "" || len(name) > 256 {
			return fmt.Errorf("invalid tool name")
		}
	}
	for server, names := range d.MCP {
		if strings.TrimSpace(server) == "" || len(server) > 128 {
			return fmt.Errorf("invalid MCP server")
		}
		for _, name := range names {
			if strings.TrimSpace(name) == "" || len(name) > 256 {
				return fmt.Errorf("invalid MCP tool")
			}
		}
	}
	return nil
}
