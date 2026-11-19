package codexadapter

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/codex"
	"github.com/Ady-JK/paladin/core/internal/harness"
)

// Codex's request_user_input_async tool asks questions without stopping the turn: it
// emits an agent message with delivery "async" and the questions, returns at once,
// and the user's reply arrives as an ordinary user message in a fixed envelope that
// names the question it answers. Paladin shows the questions as an inline card and
// sends the reply the same way Codex's own clients do.

const (
	replyStart = "<send_user_message_question_reply>"
	replyEnd   = "</send_user_message_question_reply>"
	askTool    = "request_user_input_async"
	// questionMax is how much of a question Codex keeps in a reply.
	questionMax = 512
)

// QuestionCard turns an async agent message into a question card, pending until a
// reply names it. It reports false for any other message.
func QuestionCard(item codex.Raw) (*harness.Card, bool) {
	if item.Str("type") != "agentMessage" || item.Str("delivery") != "async" || item.Str("id") == "" {
		return nil, false
	}
	var questions []harness.Question
	for _, q := range item.List("questions") {
		title := strings.TrimSpace(q.Str("title"))
		if title == "" {
			continue
		}
		questions = append(questions, harness.Question{Question: title, Options: q.Strings("options")})
	}
	if len(questions) == 0 {
		return nil, false
	}
	return &harness.Card{ID: item.Str("id"), Name: harness.CardQuestion,
		Args: jsonOf(map[string]any{"questions": questions}), Result: jsonOf(map[string]any{"status": "pending"})}, true
}

// QuestionID is how Codex names question index of the card callID in a reply.
func QuestionID(callID string, index int) string {
	return jsonOf([]any{askTool, callID, index})
}

// ParseQuestionID reads a question id back into its card and position.
func ParseQuestionID(id string) (callID string, index int, ok bool) {
	var parts []json.RawMessage
	if json.Unmarshal([]byte(id), &parts) != nil || len(parts) != 3 {
		return "", 0, false
	}
	var tool string
	if json.Unmarshal(parts[0], &tool) != nil || tool != askTool {
		return "", 0, false
	}
	if json.Unmarshal(parts[1], &callID) != nil || callID == "" || json.Unmarshal(parts[2], &index) != nil || index < 0 {
		return "", 0, false
	}
	return callID, index, true
}

// Reply is one answer inside a reply envelope.
type Reply struct {
	Answer         string `json:"answer"`
	Question       string `json:"question"`
	QuestionItemID string `json:"questionItemId"`
}

// QuestionReply is the user message that answers a card's questions, and the short
// form the conversation shows for it.
func (Adapter) QuestionReply(callID string, questions []harness.Question, answers []harness.Answer) (message, display string) {
	replies := make([]Reply, 0, len(answers))
	var shown []string
	for i, a := range answers {
		text := a.Question
		if text == "" && i < len(questions) {
			text = questions[i].Question
		}
		replies = append(replies, Reply{Answer: a.Answer, Question: boundedQuestion(text), QuestionItemID: QuestionID(callID, i)})
		shown = append(shown, "> "+boundedQuestion(text)+"\n\n"+a.Answer)
	}
	return replyStart + "\n" + encode(replies) + "\n" + replyEnd, strings.Join(shown, "\n\n")
}

// boundedQuestion keeps the question on one line and within what Codex keeps.
func boundedQuestion(q string) string {
	q = strings.NewReplacer("\r", " ", "\n", " ").Replace(q)
	if len(q) > questionMax {
		cut := questionMax
		for cut > 0 && !isRuneStart(q[cut]) {
			cut--
		}
		q = q[:cut]
	}
	return q
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// encode is JSON without HTML escaping, so a question reads as written.
func encode(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return "[]"
	}
	return strings.TrimRight(buf.String(), "\n")
}

// ParseReply reads a user message as a question reply. It reports false for any
// message that is not exactly one envelope.
func ParseReply(text string) ([]Reply, bool) {
	text = strings.TrimSpace(text)
	body, ok := strings.CutPrefix(text, replyStart)
	if !ok {
		return nil, false
	}
	body, ok = strings.CutSuffix(body, replyEnd)
	if !ok {
		return nil, false
	}
	body = strings.TrimSpace(body)
	var many []Reply
	if json.Unmarshal([]byte(body), &many) == nil && len(many) > 0 {
		return many, true
	}
	var one Reply
	if json.Unmarshal([]byte(body), &one) == nil && one.QuestionItemID != "" {
		return []Reply{one}, true
	}
	return nil, false
}

// DisplayReplies is the conversation's short form of a reply: each question quoted,
// then the answer.
func DisplayReplies(replies []Reply) string {
	shown := make([]string, 0, len(replies))
	for _, r := range replies {
		shown = append(shown, "> "+r.Question+"\n\n"+r.Answer)
	}
	return strings.Join(shown, "\n\n")
}
