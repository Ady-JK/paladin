package extension

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Plugins run under Node 24+, which strips TypeScript syntax itself.
// Electron's bundled Node works too, so a machine without Node can still
// run plugins inside the app.

// minNodeMajor is the oldest Node a plugin may run under.
const minNodeMajor = 24

// nodeProbeTimeout bounds one `node --version` probe.
const nodeProbeTimeout = 5 * time.Second

// nodeBin is a resolved Node runtime.
type nodeBin struct {
	// path is the executable (the Electron binary when runAsNode).
	path string
	// runAsNode marks Electron started with ELECTRON_RUN_AS_NODE=1, so
	// argv is just the runner script.
	runAsNode bool
}

// argv is the extra arguments a runner invocation needs. Electron runs a
// script through ELECTRON_RUN_AS_NODE, so there are none; a plain Node is
// given the script directly by the caller.
func (n nodeBin) argv() []string { return nil }

// verser reports the runtime's own version ("v24.1.0").
//
// Swapped in tests, where the shims are shell scripts.
var verser = defaultVerser

func defaultVerser(ctx context.Context, bin nodeBin) (string, error) {
	cmd := exec.CommandContext(ctx, bin.path, "--version")
	if bin.runAsNode {
		cmd.Env = append(os.Environ(), "ELECTRON_RUN_AS_NODE=1")
	}
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s --version: %s", bin.path, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("%s --version: %w", bin.path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

var versionRe = regexp.MustCompile(`^v?(\d+)\.`)

// nodeMajor parses a version string's major number.
func nodeMajor(version string) (int, error) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(version))
	if m == nil {
		return 0, fmt.Errorf("unreadable Node version %q", version)
	}
	return strconv.Atoi(m[1])
}

// runtimeEnv reads one of the environment overrides.
//
// Swapped in tests, as is pathNode.
var runtimeEnv = os.Getenv

// pathNode looks `node` up on PATH.
var pathNode = func() (string, error) { return exec.LookPath("node") }

// nodeCache remembers the PATH node probe for the process; an explicit
// override is probed every time, since it is per plugin.
var nodeCache struct {
	once sync.Once
	bin  nodeBin
	err  error
}

// resolveNode picks the Node runtime for one plugin: the manifest's own
// node, then the Electron binary the app passes down, then the operator
// override, then PATH. An explicit setting is never silently skipped: a
// broken one fails the plugin.
func resolveNode(ctx context.Context, p plugin) (nodeBin, error) {
	if p.Node != "" {
		return checkNode(ctx, nodeBin{path: p.Node}, "the plugin's node setting")
	}
	if bin := strings.TrimSpace(runtimeEnv(electronNodeEnv)); bin != "" {
		if !filepath.IsAbs(bin) {
			return nodeBin{}, fmt.Errorf("%s must be an absolute path, got %q", electronNodeEnv, bin)
		}
		return checkNode(ctx, nodeBin{path: bin, runAsNode: true}, electronNodeEnv)
	}
	if bin := strings.TrimSpace(runtimeEnv(paladinNodeEnv)); bin != "" {
		if !filepath.IsAbs(bin) {
			return nodeBin{}, fmt.Errorf("%s must be an absolute path, got %q", paladinNodeEnv, bin)
		}
		return checkNode(ctx, nodeBin{path: bin}, paladinNodeEnv)
	}
	bin, err := pathRuntime(ctx)
	if err != nil {
		return nodeBin{}, err
	}
	return bin, nil
}

const (
	electronNodeEnv = "ELECTRON_NODE"
	paladinNodeEnv  = "PALADIN_NODE"
)

// pathRuntime finds a Node 24+ on PATH, probing it once per process.
func pathRuntime(ctx context.Context) (nodeBin, error) {
	nodeCache.once.Do(func() {
		path, err := pathNode()
		if err != nil {
			nodeCache.err = fmt.Errorf("node runtime not found: install Node %d+ or run inside Paladin "+
				"(no %s and none on PATH)", minNodeMajor, paladinNodeEnv)
			return
		}
		nodeCache.bin, nodeCache.err = checkNode(context.Background(), nodeBin{path: path}, "PATH")
	})
	if nodeCache.err == nil {
		return nodeCache.bin, nil
	}
	// A cancelled caller gets its own error rather than the cached one.
	if err := ctx.Err(); err != nil {
		return nodeBin{}, err
	}
	return nodeBin{}, nodeCache.err
}

// checkNode verifies an executable exists, is executable and reports a
// version new enough, naming where the setting came from when it fails.
func checkNode(ctx context.Context, bin nodeBin, from string) (nodeBin, error) {
	info, err := os.Lstat(bin.path)
	if err != nil {
		return nodeBin{}, fmt.Errorf("%s: %s: %w", from, bin.path, err)
	}
	if info.Mode()&0o111 == 0 || info.IsDir() {
		return nodeBin{}, fmt.Errorf("%s: %s is not an executable file", from, bin.path)
	}
	probeCtx, cancel := context.WithTimeout(ctx, nodeProbeTimeout)
	defer cancel()
	version, err := verser(probeCtx, bin)
	if err != nil {
		return nodeBin{}, fmt.Errorf("%s: %w", from, err)
	}
	major, err := nodeMajor(version)
	if err != nil {
		return nodeBin{}, fmt.Errorf("%s: %w", from, err)
	}
	if major < minNodeMajor {
		return nodeBin{}, fmt.Errorf("%s: %s is Node %d; plugins need Node %d+", from, bin.path, major, minNodeMajor)
	}
	return bin, nil
}
