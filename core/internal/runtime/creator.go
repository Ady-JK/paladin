package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/creator"
)

// CreatorMode enables Paladin's extension workspace, not a security boundary
// against programs the user runs outside the application.
func (r *AgentRuntime) CreatorMode() bool { return r.loadSettings().CreatorMode }

func (r *AgentRuntime) SetCreatorMode(enabled bool) error {
	return r.updateSettings(func(s *Settings) { s.CreatorMode = enabled })
}

// CreatorWorkspace initializes the creator directories only when explicitly opened.
func (r *AgentRuntime) CreatorWorkspace() (string, error) {
	r.settingsMu.Lock()
	defer r.settingsMu.Unlock()
	if !r.CreatorMode() {
		return "", fmt.Errorf("enable Creator mode to open the extension workspace")
	}
	root, err := creator.Workspace(r.dataDir)
	if err != nil {
		return "", err
	}
	for _, dir := range []string{"ui", "core"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			return "", err
		}
	}
	return root, nil
}

// CreatorSaveIDEFile gates the app's editor. External programs and the
// user's own shell can still edit these files, as they own them.
func (r *AgentRuntime) CreatorSaveIDEFile(cwd, path, content, version string) (IDEFile, error) {
	current, err := IDEReadFile(cwd, path)
	if err != nil {
		return IDEFile{}, err
	}
	r.settingsMu.Lock()
	defer r.settingsMu.Unlock()
	if !r.CreatorMode() && r.isCreatorFile(current.Path) {
		return IDEFile{}, fmt.Errorf("enable Creator mode to edit extension files")
	}
	return IDESaveFile(cwd, path, content, version)
}

func (r *AgentRuntime) isCreatorFile(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	root, err := filepath.EvalSymlinks(filepath.Join(r.dataDir, "config", "extensions"))
	if err == nil {
		rel, relErr := filepath.Rel(root, real)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	if workspace, workspaceErr := creator.Workspace(r.dataDir); workspaceErr == nil {
		if root, rootErr := filepath.EvalSymlinks(workspace); rootErr == nil {
			rel, relErr := filepath.Rel(root, real)
			if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return true
			}
		}
	}
	configFile, err := filepath.EvalSymlinks(filepath.Join(r.dataDir, "config", "extensions.json"))
	return err == nil && real == configFile
}
