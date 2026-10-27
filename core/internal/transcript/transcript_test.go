package transcript

import (
	"os"
	"path/filepath"
	"strings"
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

// ---- merged from transcript_extra_test.go ----

func TestVisibleMatrix(t *testing.T) {
	if !UserMessage("hi").Visible() {
		t.Fatal("user hidden")
	}
	if !UserMessage("   ").Visible() {
		t.Fatal("blank user must stay visible (title skips it, history keeps it)")
	}
	if !AssistantMessage("hi").Visible() {
		t.Fatal("assistant hidden")
	}
	if AssistantMessage("   ").Visible() {
		t.Fatal("blank assistant visible")
	}
	calls := []ToolCall{{ID: "c1", Name: "read", Arguments: "{}"}}
	if !(Message{Role: RoleAssistant, Content: "  ", ToolCalls: &calls}).Visible() {
		t.Fatal("assistant with calls hidden")
	}
	if !(Message{Role: RoleTool, Content: ""}).Visible() {
		t.Fatal("tool hidden")
	}
	if !(Message{Role: RoleSystem, Content: ""}).Visible() {
		t.Fatal("system hidden")
	}
}

func TestTitleTruncation(t *testing.T) {
	if got := TitleFor(nil); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := TitleFor([]Message{UserMessage("   ")}); got != "" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("word ", 30)
	title := TitleFor([]Message{UserMessage(long)})
	if len([]rune(title)) > 81 {
		t.Fatalf("too long: %q", title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("no ellipsis: %q", title)
	}
	// First user message wins even after assistant chatter.
	msgs := []Message{AssistantMessage("hello"), UserMessage("first"), UserMessage("second")}
	if got := TitleFor(msgs); got != "first" {
		t.Fatalf("got %q", got)
	}
	short := "short title"
	if got := TitleFor([]Message{UserMessage(short)}); got != short {
		t.Fatalf("got %q", got)
	}
}

func TestAppendSetsTimestampAndPersists(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "s")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := Session{Meta: Meta{SessionID: "x", Cwd: "/tmp", CreatedAt: time.Now().UTC()}, Dir: sessDir}
	before := time.Now().Add(-time.Second)
	if err := sess.Append(RoleUser, "hi"); err != nil {
		t.Fatal(err)
	}
	msgs, err := sess.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].CreatedAt.Before(before) {
		t.Fatalf("got %+v", msgs)
	}
	// AppendMessage preserves an explicit timestamp.
	fixed := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := sess.AppendMessage(Message{Role: RoleAssistant, Content: "yo", CreatedAt: fixed}); err != nil {
		t.Fatal(err)
	}
	msgs, err = sess.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || !msgs[1].CreatedAt.Equal(fixed) {
		t.Fatalf("got %+v", msgs)
	}
}

func TestLoadTranscriptSkipsBlanksAndRejectsCorrupt(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadTranscript(dir); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	// Blank lines are skipped.
	content := "{\"role\":\"user\",\"content\":\"hi\",\"created_at\":\"2020-01-01T00:00:00Z\"}\n\n   \n"
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := LoadTranscript(dir)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("got %v %v", msgs, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte("{bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTranscript(dir); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestReadMetaVersions(t *testing.T) {
	dir := t.TempDir()
	// Missing file errors.
	if _, err := readMeta(dir); err == nil {
		t.Fatal("expected error")
	}
	// Schema 0 upgrades to 1.
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"session_id":"a","cwd":"/tmp","created_at":"2020-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := readMeta(dir)
	if err != nil || meta.SchemaVersion != SchemaVersion {
		t.Fatalf("got %+v %v", meta, err)
	}
	// Future schema rejected.
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"schema_version":99,"session_id":"a","cwd":"/tmp","created_at":"2020-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMeta(dir); err == nil || !strings.Contains(err.Error(), "version too new") {
		t.Fatalf("got %v", err)
	}
	// Corrupt JSON rejected.
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMeta(dir); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestEncodeCwdMatrix(t *testing.T) {
	a := EncodeCwd("/home/u/proj")
	b := EncodeCwd("/home/u/proj")
	if a != b {
		t.Fatal("not deterministic")
	}
	if a != EncodeCwd("/home/u/proj/")+"/x" && strings.HasSuffix(a, "/x") {
		t.Fatal("unreachable")
	}
	// Similar inputs never collide thanks to the hash suffix.
	if EncodeCwd("/a-b") == EncodeCwd("/a:b") {
		t.Fatal("collision")
	}
	if EncodeCwd("") != "_" {
		t.Fatalf("got %q", EncodeCwd(""))
	}
	for _, reserved := range []string{"CON", "con.txt", "NUL", "COM1", "lpt9"} {
		enc := EncodeCwd(reserved)
		if !strings.HasPrefix(enc, "_") {
			t.Fatalf("%q -> %q", reserved, enc)
		}
	}
	// Backslashes normalize to forward slashes.
	if EncodeCwd(`C:\proj\a`) != EncodeCwd("C:/proj/a") {
		t.Fatal("backslash not normalized")
	}
	// Legacy layout still loads: write a session in the old dir.
	store := NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create("/tmp/legacy-probe", "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(store.Root, legacyEncodeCwd("/tmp/legacy-probe"), sess.Meta.SessionID)
	fresh := filepath.Join(store.Root, EncodeCwd("/tmp/legacy-probe"), sess.Meta.SessionID)
	if fresh == legacy {
		t.Skip("layouts identical for this cwd, legacy probe N/A")
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fresh, legacy); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("/tmp/legacy-probe", sess.Meta.SessionID)
	if err != nil {
		t.Fatalf("legacy layout not found: %v", err)
	}
	if loaded.Meta.SessionID != sess.Meta.SessionID {
		t.Fatalf("got %v", loaded.Meta)
	}
}

func TestValidIDMatrix(t *testing.T) {
	if ValidID("") || ValidID(".") || ValidID("..") || ValidID("../x") || ValidID("a/b") || ValidID(`a\b`) {
		t.Fatal("bad id accepted")
	}
	if ValidID("not-a-uuid") {
		t.Fatal("non-uuid accepted")
	}
	store := NewStore(filepath.Join(t.TempDir(), "s"))
	sess, err := store.Create("/tmp/p", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(sess.Meta.SessionID) {
		t.Fatal("fresh uuid rejected")
	}
}

func TestStoreListLimitAndOrder(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "sessions"))
	if infos, err := store.List("/tmp/nowhere", 10); err != nil || len(infos) != 0 {
		t.Fatalf("got %v %v", infos, err)
	}
	var ids []string
	for i := 0; i < 3; i++ {
		s, err := store.Create("/tmp/p", "", "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.Meta.SessionID)
		time.Sleep(2 * time.Millisecond)
	}
	infos, err := store.List("/tmp/p", 2)
	if err != nil || len(infos) != 2 {
		t.Fatalf("got %v %v", infos, err)
	}
	// Newest first.
	if infos[0].Meta.CreatedAt.Before(infos[1].Meta.CreatedAt) {
		t.Fatal("wrong order")
	}
	all, err := store.List("/tmp/p", 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("limit 0 should be unlimited: %v %v", all, err)
	}
	// Empty cwd lists every namespace.
	s2, err := store.Create("/tmp/other", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = s2
	every, err := store.List("", 0)
	if err != nil || len(every) != 4 {
		t.Fatalf("got %v %v", every, err)
	}
	if _, err := store.List("/tmp/p", 10); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLoadErrors(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "sessions"))
	if _, err := store.Load("/tmp/p", ""); err == nil {
		t.Fatal("empty id")
	}
	if _, err := store.Load("/tmp/p", "../evil"); err == nil {
		t.Fatal("crafted id")
	}
	if _, err := store.Load("/tmp/p", "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("missing session should 404")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
	if _, err := store.Load("", "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("missing session should 404 without cwd")
	}
}

func TestStoreDeleteCleansNamespace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store := NewStore(root)
	sess, err := store.Create("/tmp/p", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ns := filepath.Join(root, EncodeCwd("/tmp/p"))
	if err := store.Delete("/tmp/p", sess.Meta.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ns); !os.IsNotExist(err) {
		t.Fatalf("namespace left behind: %v", err)
	}
	if err := store.Delete("/tmp/p", sess.Meta.SessionID); err == nil {
		t.Fatal("double delete should 404")
	}
	if _, err := store.Load("/tmp/p", sess.Meta.SessionID); err == nil {
		t.Fatal("deleted session loads")
	}
}

func TestInfoCountsVisibleOnly(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "s")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := Session{Meta: Meta{SessionID: "x", Cwd: "/tmp", CreatedAt: time.Now().UTC()}, Dir: sessDir}
	if err := sess.Append(RoleUser, "q"); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(RoleAssistant, "   "); err != nil {
		t.Fatal(err)
	}
	info := info(sessDir, sess.Meta)
	if info.MessageCount != 1 || info.Title != "q" {
		t.Fatalf("got %+v", info)
	}
}

func TestSessionDirValidation(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "s"))
	if _, err := store.sessionDir("/tmp/p", ""); err == nil {
		t.Fatal("empty id")
	}
	if _, err := store.sessionDir("/tmp/p", "../e"); err == nil {
		t.Fatal("crafted id")
	}
}
