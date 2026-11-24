package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

func TestForkBoundaryCrossesInvisibleAttachments(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	id := "c0de0c0d-5e55-4c0a-a111-111111111111"
	path := filepath.Join(root, "projects", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","uuid":"u1","parentUuid":null,"cwd":"/tmp/project","message":{"content":"first"}}` + "\n" + `{"type":"attachment","uuid":"internal","parentUuid":"u1"}` + "\n" + `{"type":"assistant","uuid":"a1","parentUuid":"internal","message":{"id":"m1","model":"claude-haiku-5-5","content":[{"type":"text","text":"answer"}]}}` + "\n" + `{"type":"user","uuid":"u2","parentUuid":"a1","message":{"content":"second"}}` + "\n" + `{"type":"assistant","uuid":"a2","parentUuid":"u2","message":{"id":"m2","content":[{"type":"text","text":"other"}]}}` + "\n"
	if err := atomicfile.Write(path, []byte(data)); err != nil {
		t.Fatal(err)
	}
	boundary, err := ForkBoundary(id, "u1")
	if err != nil || boundary != "a1" {
		t.Fatalf("boundary=%q err=%v", boundary, err)
	}
}
