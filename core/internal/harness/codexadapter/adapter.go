package codexadapter

import (
	"encoding/json"

	"github.com/Ady-JK/paladin/core/internal/harness"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Adapter is the Codex harness adapter.
type Adapter struct{}

// New returns the Codex adapter.
func New() Adapter { return Adapter{} }

// Name is the harness name stored in a Codex session.
func (Adapter) Name() string { return Name }

var _ harness.Adapter = Adapter{}

// Annotate settles, from the conversation alone, what a stored card could not know
// when it was written: a question the user answered later. A reply is an ordinary
// user message naming its question, so this holds across turns, restarts and an
// imported thread, and needs no state of its own. A reply row also gets its short
// display form when it has none.
func (Adapter) Annotate(msgs []transcript.Message) []transcript.Message {
	answered := map[string]map[int]Reply{} // card -> question index -> reply
	for _, m := range msgs {
		if m.Role != transcript.RoleUser {
			continue
		}
		replies, ok := ParseReply(m.Content)
		if !ok {
			continue
		}
		for _, r := range replies {
			if call, index, ok := ParseQuestionID(r.QuestionItemID); ok {
				if answered[call] == nil {
					answered[call] = map[int]Reply{}
				}
				answered[call][index] = r
			}
		}
	}
	if len(answered) == 0 && !hasPendingQuestion(msgs) {
		return msgs
	}
	out := make([]transcript.Message, len(msgs))
	copy(out, msgs)
	for i, m := range out {
		switch {
		case m.Role == transcript.RoleUser && m.Display == "":
			if replies, ok := ParseReply(m.Content); ok {
				out[i].Display = DisplayReplies(replies)
			}
		case m.Role == transcript.RoleTool && m.Name != nil && *m.Name == harness.CardQuestion && m.ToolCallID != nil:
			byIndex := answered[*m.ToolCallID]
			if len(byIndex) == 0 || !pending(m.Content) {
				continue
			}
			replies := make([]Reply, 0, len(byIndex))
			for _, r := range byIndex {
				replies = append(replies, r)
			}
			out[i].Content = AnsweredResult(replies)
		}
	}
	return out
}

func hasPendingQuestion(msgs []transcript.Message) bool {
	for _, m := range msgs {
		if m.Role == transcript.RoleTool && m.Name != nil && *m.Name == harness.CardQuestion && pending(m.Content) {
			return true
		}
	}
	return false
}

func pending(result string) bool {
	var r struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(result), &r) == nil && r.Status == "pending"
}

// AnsweredResult is a question card's result once it has replies, in question order.
func AnsweredResult(replies []Reply) string {
	indexed := map[int]Reply{}
	for _, r := range replies {
		if _, index, ok := ParseQuestionID(r.QuestionItemID); ok {
			indexed[index] = r
		}
	}
	answers := []map[string]string{}
	for i := 0; len(answers) < len(indexed) && i < len(indexed)+64; i++ {
		if r, ok := indexed[i]; ok {
			answers = append(answers, map[string]string{"question": r.Question, "answer": r.Answer})
		}
	}
	return jsonOf(map[string]any{"status": "answered", "answers": answers})
}
