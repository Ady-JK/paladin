package runtime

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestSessionAttachmentCopiesAndRestrictsImage(t *testing.T) {
	const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII="
	rt, dir := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", dir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := rt.SaveAttachment(sess.SessionID, dir, "pixel.png", pixel)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "pixel.png" || ref.Image == nil || filepath.Base(filepath.Dir(ref.Path)) != "attachments" {
		t.Fatalf("bad ref: %+v", ref)
	}
	got, err := rt.SessionAttachment(sess.SessionID, dir, ref.ID)
	if err != nil || got.ID != ref.ID {
		t.Fatalf("read ref: %+v, %v", got, err)
	}
	stored, err := rt.transcripts.Load(dir, sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := stored.AppendTurn([]transcript.Message{{Role: transcript.RoleUser, Content: "see image", Attachments: []media.Attachment{*ref}}}); err != nil {
		t.Fatal(err)
	}
	dataURL, err := rt.SessionImage(sess.SessionID, dir, ref.Path)
	if err != nil || !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Fatalf("image URL: %v", err)
	}
	if _, err := rt.SessionImage(sess.SessionID, dir, "/tmp/elsewhere.png"); err == nil {
		t.Fatal("outside path accepted")
	}
	if _, err := rt.SessionAttachment(sess.SessionID, dir, "../bad"); err == nil {
		t.Fatal("path traversal accepted")
	}
	original, err := os.ReadFile(ref.Path)
	if err != nil {
		t.Fatal(err)
	}
	original[len(original)-1] ^= 1
	if err := os.WriteFile(ref.Path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.SessionAttachment(sess.SessionID, dir, ref.ID); err == nil {
		t.Fatal("changed file accepted")
	}
	if _, err := rt.SaveAttachment(sess.SessionID, dir, "bad", base64.StdEncoding.EncodeToString(make([]byte, maxAttachmentBytes+1))); err == nil {
		t.Fatal("oversized upload accepted")
	}
}
