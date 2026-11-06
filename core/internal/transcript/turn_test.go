package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lines(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func contents(msgs []Message) string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Content)
	}
	return strings.Join(out, ",")
}

// A turn lands as one line, so a reader sees all of it or none.
func TestAppendTurnWritesOneLineAndReadsBackExpanded(t *testing.T) {
	dir := t.TempDir()
	s := &Session{Dir: dir}
	if err := s.Append(RoleUser, "legacy"); err != nil {
		t.Fatal(err)
	}
	turn := []Message{UserMessage("ask"), {Role: RoleTool, Content: "result"}, AssistantMessage("answer")}
	if err := s.AppendTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(RoleUser, "after"); err != nil {
		t.Fatal(err)
	}
	if got := lines(t, dir); len(got) != 3 || !strings.HasPrefix(got[1], `{"turn":`) {
		t.Fatalf("lines = %q", got)
	}
	msgs, err := s.History()
	if err != nil {
		t.Fatal(err)
	}
	if got := contents(msgs); got != "legacy,ask,result,answer,after" {
		t.Fatalf("history = %s", got)
	}
	for _, m := range msgs {
		if m.CreatedAt.IsZero() {
			t.Fatalf("message without a time: %+v", m)
		}
	}
}

func TestAppendTurnSkipsAnEmptyTurn(t *testing.T) {
	dir := t.TempDir()
	s := &Session{Dir: dir}
	if err := s.AppendTurn(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "messages.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("an empty turn wrote the transcript: %v", err)
	}
}

// A write that fails (here at the sync) is rolled back, so a retry or
// the next turn cannot duplicate or glue onto it.
func TestFailedAppendRollsBack(t *testing.T) {
	dir := t.TempDir()
	s := &Session{Dir: dir}
	if err := s.Append(RoleUser, "kept"); err != nil {
		t.Fatal(err)
	}
	syncFile = func(*os.File) error { return errors.New("disk full") }
	err := s.AppendTurn([]Message{UserMessage("lost"), AssistantMessage("lost too")})
	syncFile = (*os.File).Sync
	if err == nil {
		t.Fatal("failed sync swallowed")
	}
	msgs, err := s.History()
	if err != nil || contents(msgs) != "kept" {
		t.Fatalf("after a failed turn: %s, %v", contents(msgs), err)
	}
	if err := s.AppendTurn([]Message{UserMessage("retry")}); err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.History()
	if contents(msgs) != "kept,retry" {
		t.Fatalf("after retry: %s", contents(msgs))
	}
}

// A crash mid-turn leaves a torn envelope: the whole turn disappears
// and the next append repairs the file.
func TestTornTurnDisappearsWhole(t *testing.T) {
	dir := t.TempDir()
	s := &Session{Dir: dir}
	if err := s.AppendTurn([]Message{UserMessage("one"), AssistantMessage("done")}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "messages.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"turn":{"messages":[{"role":"user","content":"two"},{"role":"assis`)
	_ = f.Close()
	msgs, err := s.History()
	if err != nil || contents(msgs) != "one,done" {
		t.Fatalf("torn turn: %s, %v", contents(msgs), err)
	}
	if err := s.AppendTurn([]Message{UserMessage("three")}); err != nil {
		t.Fatal(err)
	}
	msgs, err = s.History()
	if err != nil || contents(msgs) != "one,done,three" {
		t.Fatalf("after repair: %s, %v", contents(msgs), err)
	}
}

// Undo indexes count expanded messages, envelopes included.
func TestTruncateCountsMessagesInsideTurns(t *testing.T) {
	dir := t.TempDir()
	s := &Session{Dir: dir}
	_ = s.Append(RoleUser, "legacy")
	_ = s.AppendTurn([]Message{UserMessage("a"), AssistantMessage("b")})
	_ = s.AppendTurn([]Message{UserMessage("c"), AssistantMessage("d")})
	if err := s.Truncate(3); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.History()
	if err != nil || contents(msgs) != "legacy,a,b" {
		t.Fatalf("truncated: %s, %v", contents(msgs), err)
	}
}

// A complete envelope that does not decode is corruption, not a torn
// tail.
func TestCorruptTurnLineFails(t *testing.T) {
	dir := t.TempDir()
	content := "{\"turn\":{\"messages\":[{\"role\":1}]}}\n{\"role\":\"user\",\"content\":\"hi\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTranscript(dir); err == nil {
		t.Fatal("a corrupt turn must not be skipped")
	}
}

// A turn's stats ride on its line and come back with the turn, while an
// older line without them still reads cleanly.
func TestTurnStatsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Session{Dir: dir}
	stats := &TurnStats{DurationMs: 65_000, Tokens: 12_345}
	if err := s.AppendTurnWithStats([]Message{UserMessage("ask"), AssistantMessage("answer")}, stats); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendTurn([]Message{UserMessage("plain")}); err != nil {
		t.Fatal(err)
	}
	turns, err := s.HistoryTurns()
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d", len(turns))
	}
	if turns[0].Stats == nil || turns[0].Stats.DurationMs != 65_000 || turns[0].Stats.Tokens != 12_345 {
		t.Fatalf("stats = %+v", turns[0].Stats)
	}
	if turns[1].Stats != nil {
		t.Fatalf("unexpected stats: %+v", turns[1].Stats)
	}
	msgs, statsAt := Flatten(turns)
	if len(msgs) != 3 || contents(msgs) != "ask,answer,plain" {
		t.Fatalf("messages = %s", contents(msgs))
	}
	// The turn's cost maps to its last message, the anchor every page
	// that holds any of the turn also holds. See Flatten.
	if statsAt[1] == nil || statsAt[1].Tokens != 12_345 {
		t.Fatalf("statsAt = %+v", statsAt)
	}
	if _, ok := statsAt[2]; ok {
		t.Fatalf("a turn without stats should not map: %+v", statsAt)
	}
}
