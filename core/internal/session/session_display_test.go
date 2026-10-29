package session

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/transcript"
)

func TestHistoryViewShowsDisplayForUser(t *testing.T) {
	out := HistoryView([]transcript.Message{{Role: transcript.RoleUser, Content: "expanded body", Display: "/review src"}})
	if len(out) != 1 || out[0].Content != "/review src" {
		t.Fatalf("out = %+v", out)
	}
}
