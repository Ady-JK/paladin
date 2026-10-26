package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAssistantBlankWithoutToolsIsHidden(t *testing.T) {
	if AssistantMessage("   ").Visible() {
		t.Fatal("blank assistant must be hidden")
	}
}

func TestTitleUsesFirstUserMessage(t *testing.T) {
	if got := TitleFor([]Message{UserMessage("hello world")}); got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestEncodesCwdWithHashSuffix(t *testing.T) {
	enc := EncodeCwd("/home/u/proj")
	if len(enc) < len("--home--u--proj-")+8 {
		t.Fatalf("got %q", enc)
	}
	if enc[:len("--home--u--proj-")] != "--home--u--proj-" {
		t.Fatalf("got %q", enc)
	}
}

func TestRoundTripsTranscript(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "s")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := Session{Meta: Meta{SchemaVersion: SchemaVersion, SessionID: "x", Cwd: "/tmp", CreatedAt: time.Now().UTC()}, Dir: sessDir}
	if err := sess.Append(RoleUser, "hi"); err != nil {
		t.Fatal(err)
	}
	history, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("got %d", len(history))
	}
}

func TestStoreCreateLoadListDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "sessions"))
	sess, err := store.Create("/tmp/proj", "openrouter", "m")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("/tmp/proj", sess.Meta.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Meta.Cwd != "/tmp/proj" {
		t.Fatalf("cwd %q", loaded.Meta.Cwd)
	}
	infos, err := store.List("/tmp/proj", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("got %d", len(infos))
	}
	if infos[0].Meta.Provider == nil || *infos[0].Meta.Provider != "openrouter" {
		t.Fatal("provider not persisted")
	}
	if err := store.Delete("/tmp/proj", sess.Meta.SessionID); err != nil {
		t.Fatal(err)
	}
	infos, err = store.List("/tmp/proj", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("got %d", len(infos))
	}
}

func TestRejectsCraftedIDs(t *testing.T) {
	if ValidID("../evil") || ValidID("not-a-uuid") {
		t.Fatal("crafted ids accepted")
	}
}
