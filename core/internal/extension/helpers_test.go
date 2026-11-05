package extension

import (
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// nodeFor skips a test when no Node 24+ is installed. Plugin tests need
// a real Node; the resolution tests use shims instead.
func nodeFor(t *testing.T) (string, error) {
	t.Helper()
	path, err := exec.LookPath("node")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	major, err := nodeMajor(string(out))
	if err != nil || major < minNodeMajor {
		return "", errNoNode
	}
	return path, nil
}

var errNoNode = errString("test needs Node 24 or newer")

type errString string

func (e errString) Error() string { return string(e) }

// callTool runs one tool and decodes its JSON output; text that is not
// JSON comes back as the string it is.
func callTool(t *testing.T, tool tools.Tool, args map[string]any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, callErr := tool.Call(raw, &tools.ToolCtx{Workspace: t.TempDir(), Project: t.TempDir(), SessionID: "s1"})
	if callErr != nil {
		return nil, callErr
	}
	return decodeOutput(out), nil
}

// decodeOutput turns a tool result into the value a test wants to check:
// the decoded JSON, or the raw text when it is not JSON.
func decodeOutput(out string) any {
	var value any
	if json.Unmarshal([]byte(out), &value) == nil {
		return value
	}
	return out
}

// resetNodeCache lets a test swap the node probe and see it again.
func resetNodeCache() {
	nodeCache = struct {
		once sync.Once
		bin  nodeBin
		err  error
	}{}
}

// withNode points the runtime hooks at the real PATH node and restores
// them afterwards, so a test that swapped them (or the resolution tests)
// cannot leak into the next one.
func withNode(t *testing.T) {
	t.Helper()
	if _, err := nodeFor(t); err != nil {
		t.Skip(err)
	}
	resetNodeCache()
	resetEnv()
	t.Cleanup(func() {
		resetNodeCache()
		resetEnv()
	})
}

// resetEnv restores the hooks the resolution tests swap.
func resetEnv() {
	pathNode = func() (string, error) { return exec.LookPath("node") }
	runtimeEnv = os.Getenv
	verser = defaultVerser
}
