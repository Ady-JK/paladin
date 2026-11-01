// Package extension loads explicitly enabled local TypeScript tools from
// config/extensions. Each invocation runs out of process under Node 24+.
package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

const maxOutput = 1 << 20

var moduleName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*\.(ts|mts|js|mjs)$`)
var toolName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

type configFile struct {
	Enabled []string `json:"enabled"`
}

type declaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type moduleTool struct {
	path string
	name string
	decl declaration
}

func (t *moduleTool) Name() string { return t.name }

// Extension marks the tool for the permission gate (it asks per call).
func (t *moduleTool) Extension() bool { return true }
func (t *moduleTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{Name: t.name, Description: t.decl.Description, Parameters: t.decl.Parameters}
}

func (t *moduleTool) Call(args json.RawMessage, tc *tools.ToolCtx) (string, error) {
	ctx := context.Background()
	if tc != nil && tc.Cancel != nil {
		ctx = tc.Cancel
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	input := map[string]any{"args": args, "context": map[string]string{}}
	if tc != nil {
		input["context"] = map[string]string{"workspace": tc.Workspace, "project": tc.Project, "sessionId": tc.SessionID}
	}
	data, _ := json.Marshal(input)
	out, err := run(ctx, t.path, "call", t.decl.Name, data)
	if err != nil {
		return "", tools.WithContent(`{"error":`+quoted(err.Error())+`}`, err.Error())
	}
	return string(out), nil
}

func quoted(s string) string { data, _ := json.Marshal(s); return string(data) }

// Load returns tools only from names explicitly listed in extensions.json.
// A broken module is reported and skipped; it never blocks built-in tools.
func Load(ctx context.Context, dataDir string) ([]tools.Tool, []error) {
	configPath := filepath.Join(dataDir, "config", "extensions.json")
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	if len(data) > 64<<10 {
		return nil, []error{fmt.Errorf("extensions.json is too large")}
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, []error{err}
	}
	var result []tools.Tool
	var failures []error
	seen := map[string]bool{}
	seenTools := map[string]bool{}
	for _, name := range cfg.Enabled {
		if !moduleName.MatchString(name) {
			failures = append(failures, fmt.Errorf("invalid extension name %q", name))
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		path := filepath.Join(dataDir, "config", "extensions", name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			failures = append(failures, fmt.Errorf("extension %s must be a regular file", name))
			continue
		}
		loadCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := run(loadCtx, path, "list", "", nil)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("extension %s: %w", name, err))
			continue
		}
		var decls []declaration
		if err := json.Unmarshal(out, &decls); err != nil {
			failures = append(failures, fmt.Errorf("extension %s: %w", name, err))
			continue
		}
		prefix := strings.TrimSuffix(name, filepath.Ext(name))
		for _, decl := range decls {
			var schema struct {
				Type string `json:"type"`
			}
			if !toolName.MatchString(decl.Name) || strings.TrimSpace(decl.Description) == "" || json.Unmarshal(decl.Parameters, &schema) != nil || schema.Type != "object" {
				failures = append(failures, fmt.Errorf("extension %s has invalid tool %q", name, decl.Name))
				continue
			}
			exposed := "extension__" + prefix + "__" + decl.Name
			if seenTools[exposed] {
				failures = append(failures, fmt.Errorf("duplicate extension tool %q", exposed))
				continue
			}
			seenTools[exposed] = true
			result = append(result, &moduleTool{path: path, name: exposed, decl: decl})
		}
	}
	return result, failures
}

// The script imports one enabled module, prints only protocol JSON to stdout,
// and sends console logging to stderr. Node strips erasable TS syntax itself.
const runner = `
import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
console.log = (...args) => console.error(...args);
const mod = (await import(pathToFileURL(process.argv[1]).href)).default;
if (!mod || !Array.isArray(mod.tools)) throw new Error('default export needs a tools array');
if (process.argv[2] === 'list') {
  process.stdout.write(JSON.stringify(mod.tools.map(({name, description, parameters}) => ({name, description, parameters}))));
} else {
  const tool = mod.tools.find(t => t.name === process.argv[3]);
  if (!tool || typeof tool.execute !== 'function') throw new Error('tool not found');
  const input = JSON.parse(readFileSync(0, 'utf8'));
  const value = await tool.execute(input.args, input.context);
  process.stdout.write(typeof value === 'string' ? value : JSON.stringify(value ?? null));
}
`

type cappedBuffer struct {
	bytes.Buffer
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := maxOutput - b.Len()
	if len(p) > remaining {
		b.exceeded = true
	}
	if remaining > 0 {
		b.Buffer.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}

func run(ctx context.Context, path, action, name string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "node", "--input-type=module", "-e", runner, path, action, name)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var out, stderr cappedBuffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if out.exceeded {
		return nil, fmt.Errorf("extension output exceeds %d bytes", maxOutput)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}
