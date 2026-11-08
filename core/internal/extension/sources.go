package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Ady-JK/paladin/core/internal/creator"
)

type pluginSource struct {
	root, name, exposed string
	legacy              bool
}

// sources combines explicitly enabled legacy plugins with Creator workspace
// plugins. Missing Creator folders and incomplete directories are ignored.
func (r *Registry) sources() ([]pluginSource, []error) {
	var sources []pluginSource
	var failures []error
	root := filepath.Join(r.dataDir, "config", "extensions")
	data, err := os.ReadFile(filepath.Join(r.dataDir, "config", "extensions.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		failures = append(failures, err)
	case len(data) > 64<<10:
		failures = append(failures, fmt.Errorf("extensions.json is too large"))
	default:
		var cfg configFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			failures = append(failures, err)
		} else {
			seen := map[string]bool{}
			for _, name := range cfg.Enabled {
				if seen[name] {
					continue
				}
				seen[name] = true
				sources = append(sources, pluginSource{root: root, name: name, exposed: name, legacy: moduleName.MatchString(name)})
			}
		}
	}
	workspace, err := creator.Workspace(r.dataDir)
	if err != nil {
		return sources, append(failures, err)
	}
	for _, kind := range []string{"core", "ui"} {
		dir := filepath.Join(workspace, kind)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !pluginName.MatchString(entry.Name()) || len(entry.Name()) > 48 {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, entry.Name(), ManifestName)); err != nil {
				continue
			}
			sources = append(sources, pluginSource{root: dir, name: entry.Name(), exposed: "creator_" + kind + "_" + entry.Name()})
		}
	}
	return sources, failures
}
