package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDamagedSessionIsListedAndDeletable(t *testing.T) {
	rt, _ := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	sess, err := rt.OpenSession("", ws)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := rt.transcripts.Load(ws, sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loaded.Dir, "meta.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := rt.ListSessions(ws, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SessionID != sess.SessionID || list[0].Damaged == "" {
		t.Fatalf("damaged session should be listed with a reason: %+v", list)
	}
	if ok, err := rt.DeleteSession(sess.SessionID, ""); err != nil || !ok {
		t.Fatalf("delete damaged: ok=%v err=%v", ok, err)
	}
	if list, _ := rt.ListSessions(ws, 0); len(list) != 0 {
		t.Fatalf("still listed after delete: %+v", list)
	}
}
