package agent

import (
	"testing"

	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/session"
)

func noticeChunk(id, name string) inference.Chunk {
	return inference.Chunk{ToolNotice: &inference.ToolCall{ID: id, Name: name}}
}

// lastToolStatus returns the final status reported for a tool row.
func lastToolStatus(chunks []ChatChunk, id string) string {
	status := ""
	for _, c := range chunks {
		if c.Tool != nil && c.Tool.ID == id {
			status = c.Tool.Status
		}
	}
	return status
}

func TestNoticedCallSettledWhenRoundFails(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{noticeChunk("n1", "shell")},
		err:    &inference.ProviderError{Kind: "request", Status: 400, Message: "bad request"},
	}}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err == nil {
		t.Fatal("want failure")
	}
	if got := lastToolStatus(drainChunks(tx), "n1"); got != string(session.ToolFailed) {
		t.Fatalf("noticed row left %q", got)
	}
}

func TestNoticedCallSettledWhenRoundRetried(t *testing.T) {
	fastRetries(t)
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{noticeChunk("n1", "shell")}, err: &inference.ProviderError{Kind: "request", Status: 503, Message: "overloaded"}},
		{chunks: []inference.Chunk{inference.TextChunk("ok")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	if out, err := a.Run(); err != nil || out.Reply != "ok" {
		t.Fatalf("out %+v err %v", out, err)
	}
	if got := lastToolStatus(drainChunks(tx), "n1"); got != string(session.ToolFailed) {
		t.Fatalf("noticed row left %q", got)
	}
}

func TestNoticedCallSettledWhenStoppedMidStream(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{{
		chunks: []inference.Chunk{noticeChunk("n1", "shell")},
		err:    inference.CancelledError(),
	}}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	prov.afterChunks = func(int) { a.cancel() }
	out, err := a.Run()
	if err != nil || !out.Stopped {
		t.Fatalf("out %+v err %v", out, err)
	}
	chunks := drainChunks(tx)
	if got := lastToolStatus(chunks, "n1"); got != string(session.ToolFailed) {
		t.Fatalf("noticed row left %q", got)
	}
}

func TestNoticedCallThatRunsIsNotDoubleSettled(t *testing.T) {
	prov := &mockProvider{streams: []streamScript{
		{chunks: []inference.Chunk{
			noticeChunk("c1", "read"),
			{ToolCalls: []inference.ToolCall{{ID: "c1", Name: "read", Arguments: `{"path":"nope.txt"}`}}},
		}},
		{chunks: []inference.Chunk{inference.TextChunk("done")}},
	}}
	a, _, tx, done := testAgent(t, prov)
	defer close(done)
	if _, err := a.Run(); err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, c := range drainChunks(tx) {
		if c.Tool != nil && c.Tool.ID == "c1" {
			statuses = append(statuses, c.Tool.Status)
		}
	}
	if statuses[len(statuses)-1] == string(session.ToolRunning) || len(statuses) > 3 {
		t.Fatalf("statuses %v", statuses)
	}
	if len(a.noticed) != 0 {
		t.Fatalf("leftover notices %v", a.noticed)
	}
}
