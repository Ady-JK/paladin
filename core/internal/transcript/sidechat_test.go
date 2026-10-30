package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSideChatRoundTripsUnderTheSessionFolder(t *testing.T) {
	dir := t.TempDir()
	chat := NewSideChat("What does the parser do?")
	chat.Add(RoleUser, "What does the parser do?", "")
	chat.Add(RoleAssistant, "It tokenizes.", "m1")
	if err := SaveSideChat(dir, chat); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "side-chats", chat.ID, "chat.json")); err != nil {
		t.Fatalf("chat not stored under side-chats/<uuid>: %v", err)
	}
	got, err := LoadSideChat(dir, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "What does the parser do?" || len(got.Messages) != 2 || got.Messages[1].Model != "m1" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestListSideChatsNewestFirstAndSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	first := NewSideChat("first")
	first.Add(RoleUser, "first", "")
	if err := SaveSideChat(dir, first); err != nil {
		t.Fatal(err)
	}
	second := NewSideChat("second")
	second.Add(RoleUser, "second", "")
	second.UpdatedAt = first.UpdatedAt.Add(1e9)
	if err := SaveSideChat(dir, second); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "side-chats", "not-a-chat"), 0o700); err != nil {
		t.Fatal(err)
	}
	list, err := ListSideChats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].MessageCount != 1 {
		t.Fatalf("list = %+v", list)
	}
}

func TestListSideChatsWithNoFolderIsEmpty(t *testing.T) {
	list, err := ListSideChats(t.TempDir())
	if err != nil || len(list) != 0 {
		t.Fatalf("list = %v, %v", list, err)
	}
}

func TestSideChatIDsMustBeUUIDs(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadSideChat(dir, "../meta"); err == nil {
		t.Fatal("a path-like id must be rejected")
	}
	if err := DeleteSideChat(dir, "../../x"); err == nil {
		t.Fatal("a path-like id must be rejected on delete")
	}
}

func TestDeleteSideChat(t *testing.T) {
	dir := t.TempDir()
	chat := NewSideChat("q")
	if err := SaveSideChat(dir, chat); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSideChat(dir, chat.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSideChat(dir, chat.ID); err == nil {
		t.Fatal("deleted chat still loads")
	}
}

func TestSideChatTitleIsShortened(t *testing.T) {
	long := ""
	for range 30 {
		long += "word "
	}
	if c := NewSideChat(long); len([]rune(c.Title)) > 61 {
		t.Fatalf("title too long: %q", c.Title)
	}
}
