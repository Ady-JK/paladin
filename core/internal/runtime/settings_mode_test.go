package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsAreWrittenOwnerOnlyAndLeaveNoTempFile(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.updateSettings(func(s *Settings) {}); err != nil {
		t.Fatal(err)
	}
	path := rt.settingsPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("settings.json mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got&0o077 != 0 {
		t.Fatalf("settings folder mode = %o, want owner-only", got)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*.tmp"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
	// Updating again keeps the mode (atomic replace must not widen it).
	if err := rt.updateSettings(func(s *Settings) {}); err != nil {
		t.Fatal(err)
	}
	if info, _ = os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json mode after rewrite = %o", info.Mode().Perm())
	}
}
