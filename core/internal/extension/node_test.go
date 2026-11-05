package extension

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shimNode writes a fake node whose --version prints version.
func shimNode(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "node")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo '" + version + "'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// A plugin's own node setting is used, and a broken one fails loudly
// instead of falling through to another runtime.
func TestResolveNodeUsesPluginSetting(t *testing.T) {
	withNode(t)
	ctx := context.Background()
	good := shimNode(t, "v24.3.0")
	bin, err := resolveNode(ctx, plugin{name: "p", Node: good})
	if err != nil || bin.path != good || bin.runAsNode {
		t.Fatalf("bin %+v err %v", bin, err)
	}
	if _, err := resolveNode(ctx, plugin{name: "p", Node: filepath.Join(t.TempDir(), "missing")}); err == nil ||
		!strings.Contains(err.Error(), "the plugin's node setting") {
		t.Fatalf("a missing explicit node must fail loudly: %v", err)
	}
	// Not executable.
	notExec := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveNode(ctx, plugin{Node: notExec}); err == nil || !strings.Contains(err.Error(), "not an executable") {
		t.Fatalf("non-executable node accepted: %v", err)
	}
}

// ELECTRON_NODE is used with the run-as-node flag; PALADIN_NODE without.
func TestResolveNodeEnvironmentChain(t *testing.T) {
	withNode(t)
	ctx := context.Background()
	electron := shimNode(t, "v24.0.0")
	t.Setenv(electronNodeEnv, electron)
	bin, err := resolveNode(ctx, plugin{name: "p"})
	if err != nil || bin.path != electron || !bin.runAsNode {
		t.Fatalf("ELECTRON_NODE: %+v %v", bin, err)
	}
	// A relative or missing ELECTRON_NODE is an error, not a skip.
	t.Setenv(electronNodeEnv, "electron")
	if _, err := resolveNode(ctx, plugin{}); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("relative ELECTRON_NODE accepted: %v", err)
	}
	t.Setenv(electronNodeEnv, "")
	override := shimNode(t, "v24.1.0")
	t.Setenv(paladinNodeEnv, override)
	bin, err = resolveNode(ctx, plugin{name: "p"})
	if err != nil || bin.path != override || bin.runAsNode {
		t.Fatalf("PALADIN_NODE: %+v %v", bin, err)
	}
	// The manifest's own node wins over both.
	own := shimNode(t, "v25.0.0")
	bin, err = resolveNode(ctx, plugin{Node: own})
	if err != nil || bin.path != own {
		t.Fatalf("manifest node should win: %+v %v", bin, err)
	}
}

// A PATH node older than 24 is refused with a message that says what to
// do about it, and the probe is cached for the process.
func TestResolveNodePathVersionGate(t *testing.T) {
	withNode(t)
	t.Setenv(electronNodeEnv, "")
	t.Setenv(paladinNodeEnv, "")
	old := shimNode(t, "v20.11.0")
	probes := 0
	pathNode = func() (string, error) { probes++; return old, nil }
	resetNodeCache()
	if _, err := resolveNode(context.Background(), plugin{}); err == nil ||
		!strings.Contains(err.Error(), "plugins need Node 24+") {
		t.Fatalf("old node accepted: %v", err)
	}
	if _, err := resolveNode(context.Background(), plugin{}); err == nil {
		t.Fatal("expected the cached failure")
	}
	if probes != 1 {
		t.Fatalf("PATH probed %d times, want 1 (cached)", probes)
	}
	// No node at all names the fallback.
	pathNode = func() (string, error) { return "", os.ErrNotExist }
	resetNodeCache()
	if _, err := resolveNode(context.Background(), plugin{}); err == nil ||
		!strings.Contains(err.Error(), "node runtime not found") {
		t.Fatalf("missing node: %v", err)
	}
}

func TestResolveNodeAcceptsNewEnough(t *testing.T) {
	withNode(t)
	t.Setenv(electronNodeEnv, "")
	t.Setenv(paladinNodeEnv, "")
	pathNode = func() (string, error) { return shimNode(t, "v24.0.0"), nil }
	resetNodeCache()
	bin, err := resolveNode(context.Background(), plugin{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(bin.path, "node") || bin.runAsNode {
		t.Fatalf("%+v", bin)
	}
}

func TestNodeMajor(t *testing.T) {
	for in, want := range map[string]int{"v24.1.0": 24, "24.0.0": 24, " v25.2.3 ": 25} {
		got, err := nodeMajor(in)
		if err != nil || got != want {
			t.Fatalf("%q: %d %v", in, got, err)
		}
	}
	if _, err := nodeMajor("not a version"); err == nil {
		t.Fatal("garbage version accepted")
	}
}
