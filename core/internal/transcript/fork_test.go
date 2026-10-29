package transcript

import (
	"strings"
	"testing"
)

func TestForkCopiesHistoryAndLinksParent(t *testing.T) {
	store := NewStore(t.TempDir())
	src, err := store.Create("/w", "p", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = src.Append(RoleUser, "hello there")
	_ = src.Append(RoleAssistant, "hi")
	fork, err := store.Fork("/w", src.Meta.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if fork.Meta.SessionID == src.Meta.SessionID || fork.Meta.ForkedFrom != src.Meta.SessionID {
		t.Fatalf("fork meta = %+v", fork.Meta)
	}
	if fork.Meta.Provider == nil || *fork.Meta.Provider != "p" {
		t.Fatal("provider not copied")
	}
	msgs, _ := fork.History()
	if len(msgs) != 2 || msgs[0].Content != "hello there" {
		t.Fatalf("history = %+v", msgs)
	}
	_ = fork.Append(RoleUser, "only in fork")
	orig, _ := src.History()
	if len(orig) != 2 {
		t.Fatal("fork wrote into the source")
	}
	infos, _ := store.List("/w", 0)
	if len(infos) != 2 || !strings.HasSuffix(infos[0].Title, "(fork)") {
		t.Fatalf("list = %+v", infos)
	}
	if _, err := store.Fork("/w", "00000000-0000-4000-8000-000000000999"); err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestRenameOverridesDerivedTitle(t *testing.T) {
	store := NewStore(t.TempDir())
	s, _ := store.Create("/w", "", "")
	_ = s.Append(RoleUser, "derived title")
	if err := store.Rename("/w", s.Meta.SessionID, "  My Session  "); err != nil {
		t.Fatal(err)
	}
	infos, _ := store.List("/w", 0)
	if infos[0].Title != "My Session" {
		t.Fatalf("title = %q", infos[0].Title)
	}
	if err := store.Rename("/w", s.Meta.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	infos, _ = store.List("/w", 0)
	if infos[0].Title != "derived title" {
		t.Fatalf("cleared title = %q", infos[0].Title)
	}
	if err := store.Rename("/w", s.Meta.SessionID, strings.Repeat("x", 300)); err == nil {
		t.Fatal("overlong title accepted")
	}
}

func TestTitlePrefersDisplay(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "long expanded prompt body", Display: "/review"}}
	if got := TitleFor(msgs); got != "/review" {
		t.Fatalf("title = %q", got)
	}
}
