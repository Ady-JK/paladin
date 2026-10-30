package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// steerInbox is a test SteerSource fed from provider hooks.
type steerInbox struct{ pending []SteerMessage }

func (s *steerInbox) take() []SteerMessage {
	out := s.pending
	s.pending = nil
	return out
}

func lastUser(msgs []inference.LlmMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func TestSteerJoinsBeforeNextModelCall(t *testing.T) {
	inbox := &steerInbox{}
	prov := &mockProvider{streams: []streamScript{
		callRound("c1", "read", `{"path":"/nope"}`),
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	prov.afterChunks = func(idx int) {
		if idx == 0 {
			inbox.pending = append(inbox.pending, SteerMessage{Message: "also check y", Display: "check y"})
		}
	}
	a, sess, tx, done := testAgent(t, prov)
	defer close(done)
	a.steer = inbox.take
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 || lastUser(prov.streamMsgs[1]) != "also check y" {
		t.Fatalf("steer not sent on the next call: calls=%d %+v", prov.calls, prov.streamMsgs)
	}
	var steered []string
	for _, c := range drainChunks(tx) {
		if c.Steer != nil {
			steered = append(steered, c.Steer.Message)
		}
	}
	if len(steered) != 1 || steered[0] != "also check y" {
		t.Fatalf("steer chunk %v", steered)
	}
	h, _ := sess.History()
	var users []string
	for _, m := range h {
		if m.Role == transcript.RoleUser {
			users = append(users, m.Content+"|"+m.Display)
		}
	}
	if len(users) != 2 || users[0] != "hi|" || users[1] != "also check y|check y" {
		t.Fatalf("steer must persist in order after the turn's message: %v", users)
	}
}

func TestSteerAfterFinalAnswerKeepsTurnGoing(t *testing.T) {
	inbox := &steerInbox{}
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{inference.TextChunk("first")}},
		{chunks: []inference.Chunk{inference.TextChunk("second")}},
	}}
	prov.afterChunks = func(idx int) {
		if idx == 0 {
			inbox.pending = append(inbox.pending, SteerMessage{Message: "one more thing"})
		}
	}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	a.steer = inbox.take
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	if prov.calls != 2 || lastUser(prov.streamMsgs[1]) != "one more thing" {
		t.Fatalf("a steer that lands as the model answers must continue the turn: calls=%d", prov.calls)
	}
}

func TestNoSteerSourceIsFine(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{chunks: []inference.Chunk{inference.TextChunk("ok")}}}}
	a, _, _, done := testAgent(t, prov)
	defer close(done)
	if out, err := a.Run(); err != nil || out.Reply != "ok" {
		t.Fatalf("%+v %v", out, err)
	}
}
