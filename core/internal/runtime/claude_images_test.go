package runtime

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/agent"
)

func TestClaudeImageAttachmentSurvivesNativeTurn(t *testing.T) {
	rt, _ := claudeRuntime(t)
	id, cwd := openClaude(t, rt)
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	attachment, err := rt.SaveAttachment(id, cwd, "image.png", base64.StdEncoding.EncodeToString(pngBytes.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	provider, model := HarnessClaude, "claude-haiku-5-5"
	_, done, err := rt.SubmitTurn(agent.Request{SessionID: id, Cwd: cwd, Provider: &provider, Model: &model, Message: "describe the image", Permissions: "read-only", AttachmentIDs: []string{attachment.ID}}, SubmitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	final := awaitTurn(t, done)
	if final.Error != "" || !final.Persisted {
		t.Fatalf("image turn=%+v", final)
	}
	history, err := rt.SessionHistory(id, cwd)
	if err != nil || len(history) == 0 || len(history[0].Attachments) != 1 {
		t.Fatalf("image attachment lost: rows=%d err=%v", len(history), err)
	}
}
