package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
)

func TestConversationFollowsActiveBranch(t *testing.T) {
	entries := []Raw{Decode([]byte(`{"type":"user","uuid":"u1","parentUuid":null,"message":{"content":"first"}}`)), Decode([]byte(`{"type":"assistant","uuid":"abandoned","parentUuid":"u1","message":{"content":[]}}`)), Decode([]byte(`{"type":"assistant","uuid":"a2","parentUuid":"u1","message":{"content":[]}}`))}
	active := Conversation(entries)
	if len(active) != 2 || active[1].Str("uuid") != "a2" {
		t.Fatalf("abandoned branch retained: %v", active)
	}
}
func TestDiscoverAndReadSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	id := "c0de0c0d-5e55-4c0a-a111-111111111111"
	path := filepath.Join(root, "projects", "project", id+".jsonl")
	data := `{"type":"user","uuid":"u1","parentUuid":null,"cwd":"/tmp/project","message":{"content":"hello"}}` + "\n" + `{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"model":"claude-haiku-5-5","content":[{"type":"text","text":"world"}]}}` + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(path, []byte(data)); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessions("/tmp/project", "hello")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%v err=%v", sessions, err)
	}
	_, entries, err := ReadSession(id)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	if _, _, err = ReadSession("../private"); err == nil {
		t.Fatal("path traversal session accepted")
	}
}
