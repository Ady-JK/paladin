package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/agentconfig"
	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

var agentSeedMu sync.Mutex

// seedAgentDefinitions creates editable defaults without replacing existing files.
func (r *AgentRuntime) seedAgentDefinitions() error {
	root, err := agentconfig.Dir(r.dataDir)
	if err != nil {
		return err
	}
	agentSeedMu.Lock()
	defer agentSeedMu.Unlock()
	for _, kind := range subagentKinds {
		dir := filepath.Join(root, kind.Name)
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if info, e := os.Lstat(dir); e != nil || !info.IsDir() {
			return fmt.Errorf("agent directory %s is not a real directory", dir)
		}
		path := filepath.Join(dir, agentconfig.FileName)
		if _, err = os.Lstat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		allowed := make(map[string]bool, len(kind.Tools))
		for _, tool := range kind.Tools {
			allowed[tool] = true
		}
		d := agentconfig.Definition{Name: kind.Name, Description: kind.Description, Permissions: "read-only", Tools: allowed, MCP: map[string][]string{}, Role: kind.Role}
		data, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			return err
		}
		if err = atomicfile.Create(path, append(data, '\n')); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func (r *AgentRuntime) subagentDefinition(sa *transcript.Subagent) (subagentKind, bool) {
	if d := sa.Definition; d != nil {
		return subagentKind{Name: sa.Type, Description: d.Description, Role: d.Role, Tools: d.Tools, MCP: d.MCP, ReadWrite: d.ReadWrite, FileBased: d.FileBased}, true
	}
	return r.findConfiguredSubagent(sa.Type)
}

func (r *AgentRuntime) configuredSubagents() []subagentKind {
	root, err := agentconfig.Dir(r.dataDir)
	if err != nil {
		return nil
	}
	defs, _ := agentconfig.List(root)
	out := make([]subagentKind, 0, len(defs))
	for _, d := range defs {
		if !subagentKindVisible(d.Name) {
			continue
		}
		k := subagentKind{Name: d.Name, Description: d.Description, Role: d.Role, ReadWrite: d.Permissions == "read-write", MCP: d.MCP, FileBased: true}
		for name, on := range d.Tools {
			if on {
				k.Tools = append(k.Tools, name)
			}
		}
		sort.Strings(k.Tools)
		// Preserve prompt overrides previously saved in Settings → Customize.
		if _, builtin := findSubagentKind(k.Name); builtin {
			if state, e := custom.Get("subagent." + k.Name); e == nil && state.Customized {
				k.Role = state.Value
			}
		}
		out = append(out, k)
	}
	return out
}

func (r *AgentRuntime) findConfiguredSubagent(name string) (subagentKind, bool) {
	if !subagentKindVisible(name) {
		// The automatic memory reviewer remains confined to its original
		// tool set even when its role is edited in its definition file.
		kind, ok := findSubagentKind(name)
		if !ok {
			return subagentKind{}, false
		}
		if root, err := agentconfig.Dir(r.dataDir); err == nil {
			defs, _ := agentconfig.List(root)
			for _, d := range defs {
				if d.Name == name {
					kind.Role = d.Role
					kind.FileBased = true
					break
				}
			}
		}
		if state, err := custom.Get("subagent." + name); err == nil && state.Customized {
			kind.Role = state.Value
		}
		return kind, true
	}
	for _, kind := range r.configuredSubagents() {
		if strings.EqualFold(kind.Name, name) {
			return kind, true
		}
	}
	return subagentKind{}, false
}

func (r *AgentRuntime) initAgentDefinitions() {
	if err := r.seedAgentDefinitions(); err != nil {
		fmt.Fprintf(os.Stderr, "subagents: cannot initialize definitions: %v\n", err)
	}
}
