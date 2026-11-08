package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/creator"
)

// IsCreatorSkill reports a reserved Creator skill name, including user overrides, so every
// route to the skill catalog and loader observes the same mode boundary.
func IsCreatorSkill(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "writing-an-extension", "writing-a-plugin-ui", "creating-paladin-ui":
		return true
	default:
		return false
	}
}

func creatorSkillsAvailable(dataDir, cwd string) bool {
	if dataDir == "" || !filepath.IsAbs(cwd) {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "config", "settings.json"))
	if err != nil {
		return false
	}
	var settings struct {
		CreatorMode bool `json:"creatorMode"`
	}
	if json.Unmarshal(data, &settings) != nil || !settings.CreatorMode {
		return false
	}
	root, err := creator.Workspace(dataDir)
	if err != nil {
		return false
	}
	root, cwd = canonicalPath(root), canonicalPath(cwd)
	rel, err := filepath.Rel(root, cwd)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonicalPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}
