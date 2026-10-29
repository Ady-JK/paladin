package transcript

import (
	"encoding/json"
	"testing"
)

func TestReplayMessagesAreHidden(t *testing.T) {
	m := UserMessage("again")
	m.Replay = true
	if m.Visible() {
		t.Fatal("replayed tail copies must not show in history")
	}
	if TitleFor([]Message{m, UserMessage("real")}) != "real" {
		t.Fatal("title must skip replays")
	}
}

func TestCompactionMarkerRoundTrips(t *testing.T) {
	m := Message{Role: RoleSystem, Content: "sum", Compaction: &Compaction{TokensBefore: 1200, Reason: "auto"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Message
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Compaction == nil || back.Compaction.TokensBefore != 1200 || back.Compaction.Reason != "auto" {
		t.Fatalf("got %+v", back.Compaction)
	}
	if !m.IsCompaction() || UserMessage("x").IsCompaction() {
		t.Fatal("IsCompaction")
	}
}
