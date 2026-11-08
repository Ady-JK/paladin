// Package creator owns the user-controlled Creator workspace location.
package creator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/config"
)

// Workspace returns the shared Creator workspace. The override lets tests and
// managed environments isolate it without changing the user's home folder.
func Workspace(dataDir string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("PALADIN_CREATOR_DIR")); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("PALADIN_CREATOR_DIR must be absolute")
		}
		return filepath.Clean(override), nil
	}
	// An isolated data directory must never pick up the user's real plugins.
	if strings.TrimSpace(os.Getenv("PALADIN_DATA_DIR")) != "" || filepath.Clean(dataDir) != filepath.Clean(config.DataDir()) {
		return filepath.Join(dataDir, "creator"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "paladin", "creator"), nil
}
