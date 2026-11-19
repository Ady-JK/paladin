package tools

import (
	"runtime"
	"testing"
)

// macOS and Windows ignore case in paths, so a path spelled in another case
// is still the workspace; treating it as outside would skip the checks
// that only run on workspace files.
func TestScopeIgnoresCaseOnCaseBlindSystems(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("this file system is case-sensitive")
	}
	ws := t.TempDir()
	upper := []rune(ws)
	for i, r := range upper {
		if r >= 'a' && r <= 'z' {
			upper[i] = r - 32
		}
	}
	if got := scopeOf(cleanPath(string(upper)+"/.GIT/hooks/pre-commit"), ws); got != ScopeInside {
		t.Fatalf("differently-cased workspace path = %q, want inside", got)
	}
	if got := scopeOf(cleanPath(ws+"-sibling/file"), ws); got != ScopeOutside {
		t.Fatalf("sibling folder = %q, want outside", got)
	}
}
