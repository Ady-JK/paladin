package runtime

import (
	"fmt"
	"testing"

	"github.com/Ady-JK/paladin/core/internal/media"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// seedTurns appends n user+assistant pairs to a session's transcript.
func seedTurns(t *testing.T, rt *AgentRuntime, dir, sessionID string, n int) {
	t.Helper()
	stored, err := rt.transcripts.Load(dir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		turn := []transcript.Message{
			{Role: transcript.RoleUser, Content: fmt.Sprintf("q%d", i)},
			{Role: transcript.RoleAssistant, Content: fmt.Sprintf("a%d", i)},
		}
		if err := stored.AppendTurn(turn); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionHistoryPageSlicesAndKeepsOlderPagesStable(t *testing.T) {
	rt, dir := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", dir)
	if err != nil {
		t.Fatal(err)
	}
	seedTurns(t, rt, dir, sess.SessionID, 5)

	head, err := rt.SessionHistoryPage(sess.SessionID, dir, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if head.Total != 10 || head.Start != 6 || len(head.Messages) != 4 || !head.HasMore {
		t.Fatalf("head page = %+v", head)
	}
	if got := head.Messages[0].Content; got != "q3" {
		t.Fatalf("head starts at %q, want q3", got)
	}

	older, err := rt.SessionHistoryPage(sess.SessionID, dir, 4, head.Start)
	if err != nil {
		t.Fatal(err)
	}
	if older.Start != 2 || len(older.Messages) != 4 || !older.HasMore || older.Messages[0].Content != "q1" {
		t.Fatalf("older page = %+v", older)
	}

	oldest, err := rt.SessionHistoryPage(sess.SessionID, dir, 4, older.Start)
	if err != nil {
		t.Fatal(err)
	}
	if oldest.Start != 0 || len(oldest.Messages) != 2 || oldest.HasMore || oldest.Messages[0].Content != "q0" {
		t.Fatalf("oldest page = %+v", oldest)
	}

	// A turn landing at the end must not shift what an older cursor
	// points at: rows are numbered from the start of the session.
	seedTurns(t, rt, dir, sess.SessionID, 1)
	again, err := rt.SessionHistoryPage(sess.SessionID, dir, 4, head.Start)
	if err != nil {
		t.Fatal(err)
	}
	if again.Start != 2 || again.Messages[0].Content != "q1" {
		t.Fatalf("older page after append = %+v", again)
	}
	if again.Total != 12 {
		t.Fatalf("total after append = %d, want 12", again.Total)
	}
}

func TestSessionImagesReportsOnlyImageRows(t *testing.T) {
	rt, dir := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := rt.transcripts.Load(dir, sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	shot := &media.Image{Path: "/sessions/shot.png", MIMEType: "image/png", Size: 10, SHA256: "x"}
	turn := []transcript.Message{
		{
			Role: transcript.RoleUser, Content: "look",
			Attachments: []media.Attachment{{
				ID: "a1", Name: "look.png", Path: "/sessions/look.png", MIMEType: "image/png",
				Image: &media.Image{Path: "/sessions/look.png", MIMEType: "image/png", Size: 4, SHA256: "y"},
			}},
		},
		{Role: transcript.RoleAssistant, Content: "here", ToolCalls: &[]transcript.ToolCall{{ID: "c1", Name: "browser"}}},
		{Role: transcript.RoleTool, Content: "{}", ToolCallID: ptr("c1"), Image: shot},
		{Role: transcript.RoleUser, Content: "thanks"},
	}
	if err := stored.AppendTurn(turn); err != nil {
		t.Fatal(err)
	}
	view, err := rt.SessionImages(sess.SessionID, dir)
	if err != nil {
		t.Fatal(err)
	}
	if view.LatestUser != 3 {
		t.Fatalf("latestUser = %d, want 3", view.LatestUser)
	}
	if len(view.Images) != 2 {
		t.Fatalf("images = %+v", view.Images)
	}
	if view.Images[0].Path != "/sessions/look.png" || view.Images[0].Name != "look.png" || view.Images[0].Role != "user" {
		t.Fatalf("user attachment = %+v", view.Images[0])
	}
	if view.Images[1].Path != "/sessions/shot.png" || view.Images[1].Index != 2 {
		t.Fatalf("tool screenshot = %+v", view.Images[1])
	}
}

func ptr[T any](v T) *T { return &v }

// A turn's persisted stats reach the history row the renderer anchors
// the summary on: the turn's last row, which every page holding any part
// of that turn contains.
func TestSessionHistoryCarriesTurnStats(t *testing.T) {
	rt, dir := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := rt.transcripts.Load(dir, sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	stats := &transcript.TurnStats{DurationMs: 65_000, Tokens: 1_234}
	if err := stored.AppendTurnWithStats([]transcript.Message{
		{Role: transcript.RoleUser, Content: "q"},
		{Role: transcript.RoleAssistant, Content: "a"},
	}, stats); err != nil {
		t.Fatal(err)
	}
	history, err := rt.SessionHistory(sess.SessionID, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("rows = %d", len(history))
	}
	if history[1].Stats == nil || history[1].Stats.DurationMs != 65_000 || history[1].Stats.Tokens != 1_234 {
		t.Fatalf("stats = %+v", history[1].Stats)
	}
	if history[0].Stats != nil {
		t.Fatalf("the user row should not carry the summary: %+v", history[0].Stats)
	}
}

// A page that starts in the middle of a turn still carries that turn's
// summary, because the turn's end is in the page even when its start is
// not. This is what a restarted app shows for a long session.
func TestSessionHistoryPageKeepsStatsForTheLastTurn(t *testing.T) {
	rt, dir := testRuntime(t)
	if err := rt.store.Keys.Set("openrouter", "sk-test"); err != nil {
		t.Fatal(err)
	}
	sess, err := rt.OpenSession("", dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := rt.transcripts.Load(dir, sess.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	// One long turn: a user row, then a run of tool rows, then the reply
	// that ends it.
	msgs := []transcript.Message{{Role: transcript.RoleUser, Content: "long"}}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, transcript.Message{Role: transcript.RoleTool, Content: "{}", Name: ptr("shell")})
	}
	msgs = append(msgs, transcript.Message{Role: transcript.RoleAssistant, Content: "done"})
	stats := &transcript.TurnStats{DurationMs: 90_000, Tokens: 4_321}
	if err := stored.AppendTurnWithStats(msgs, stats); err != nil {
		t.Fatal(err)
	}
	// A page of the newest 3 rows begins mid-turn: the user row is gone.
	page, err := rt.SessionHistoryPage(sess.SessionID, dir, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 3 {
		t.Fatalf("page rows = %d", len(page.Messages))
	}
	found := false
	for _, row := range page.Messages {
		if row.Stats != nil {
			found = true
			if row.Stats.Tokens != 4_321 {
				t.Fatalf("stats = %+v", row.Stats)
			}
		}
	}
	if !found {
		t.Fatalf("the page lost the turn summary: %+v", page.Messages)
	}
	last := page.Messages[len(page.Messages)-1]
	if last.Stats == nil || last.Role != "assistant" {
		t.Fatalf("summary should sit on the turn's last row: %+v", last)
	}
}
