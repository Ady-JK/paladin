// ask.go asks the user questions mid-turn.
//
// The model calls ask with an array of questions (each with up to three
// options); the call blocks until the desktop UI resolves it through the
// answerAsk sidecar method. The UI shows one question at a time with
// back/forward navigation and a final review screen, then every question
// with its answer comes back as the tool result so the turn continues.
// Cancelling the wizard stops the turn; stopping the turn interrupts the
// blocked call like any other cancellation.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

const (
	// MaxAskQuestions caps one ask call so the review screen stays usable.
	MaxAskQuestions = 10
	// MaxAskOptions caps the options per question; a custom typed answer
	// is always available in the UI on top of these.
	MaxAskOptions = 6
)

// AskQuestion is one model-posed question with up to MaxAskOptions options.
type AskQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	// Multiple lets the user pick several options (checkboxes).
	Multiple bool `json:"multiple,omitempty"`
}

// AskAnswer pairs a question with the user's answer.
type AskAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	// Selected lists every chosen option for multiple-choice questions;
	// Answer then joins them with ", ".
	Selected []string `json:"selected,omitempty"`
}

type askOutcome struct {
	answers   []AskAnswer
	cancelled bool
}

type askPending struct {
	questions []AskQuestion
	done      chan askOutcome
}

// AskTool blocks the turn on user questions until the UI answers.
// One instance is shared by every turn of the runtime (like the shell
// tool); pending calls key on session plus call id so concurrent turns
// never cross answers.
type AskTool struct {
	mu      sync.Mutex
	pending map[string]*askPending
}

// NewAskTool builds an AskTool with no pending questions.
func NewAskTool() *AskTool {
	return &AskTool{pending: map[string]*askPending{}}
}

func askKey(sessionID, callID string) string {
	return sessionID + "\x00" + callID
}

func (*AskTool) Name() string { return "ask" }

func (*AskTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "ask",
		Description: "Ask the user questions when you are blocked on a decision " +
			"only they can make. Pass an array of questions; each has a question " +
			"and up to 6 options; set multiple=true when several options may apply " +
			"(the user then picks any number). The call waits while the user answers one " +
			"question at a time in the UI (a custom typed answer is always " +
			"available besides the options) and returns every question with its " +
			"answer so the turn can continue. Use sparingly: discover answers " +
			"with read and shell first, and batch related questions into one " +
			"call instead of asking one at a time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"description": "The questions to ask, in order. At most 10 per call.",
					"minItems":    1,
					"maxItems":    MaxAskQuestions,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{"type": "string", "description": "The question to show the user."},
							"options": map[string]any{
								"type":        "array",
								"description": "Up to 6 suggested answers; the user can always type their own instead.",
								"items":       map[string]any{"type": "string"},
								"maxItems":    MaxAskOptions,
							},
							"multiple": map[string]any{"type": "boolean", "description": "Allow selecting several options."},
						},
						"required": []string{"question"},
					},
				},
			},
			"required": []string{"questions"},
		},
	}
}

// parseAskQuestions validates the raw arguments into trimmed questions.
func parseAskQuestions(raw json.RawMessage) ([]AskQuestion, error) {
	var args struct {
		Questions []AskQuestion `json:"questions"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, ArgsError(fmt.Sprintf("ask: the arguments are not valid JSON: %v", err))
	}
	if len(args.Questions) == 0 {
		return nil, ArgsError("ask: questions is required and must not be empty")
	}
	if len(args.Questions) > MaxAskQuestions {
		return nil, ArgsError(fmt.Sprintf("ask: at most %d questions per call, got %d", MaxAskQuestions, len(args.Questions)))
	}
	out := make([]AskQuestion, 0, len(args.Questions))
	for i, q := range args.Questions {
		question := strings.TrimSpace(q.Question)
		if question == "" {
			return nil, ArgsError(fmt.Sprintf("ask: question %d is empty", i+1))
		}
		if len(q.Options) > MaxAskOptions {
			return nil, ArgsError(fmt.Sprintf("ask: question %d has %d options, at most %d allowed", i+1, len(q.Options), MaxAskOptions))
		}
		opts := make([]string, 0, len(q.Options))
		for j, opt := range q.Options {
			opt = strings.TrimSpace(opt)
			if opt == "" {
				return nil, ArgsError(fmt.Sprintf("ask: question %d option %d is empty", i+1, j+1))
			}
			opts = append(opts, opt)
		}
		out = append(out, AskQuestion{Question: question, Options: opts, Multiple: q.Multiple && len(opts) > 1})
	}
	return out, nil
}

// summarizeAsk pairs the stored questions with the user's answers for the
// model-facing result. Answers come from the UI in question order.
func summarizeAsk(questions []AskQuestion, answers []AskAnswer) (string, error) {
	if len(answers) != len(questions) {
		return "", ArgsError(fmt.Sprintf("ask: got %d answers for %d questions", len(answers), len(questions)))
	}
	pairs := make([]AskAnswer, 0, len(questions))
	for i, q := range questions {
		var selected []string
		for _, s := range answers[i].Selected {
			if s = strings.TrimSpace(s); s != "" {
				selected = append(selected, s)
			}
		}
		if len(selected) > 1 && !q.Multiple {
			return "", ArgsError(fmt.Sprintf("ask: question %d allows one answer, got %d", i+1, len(selected)))
		}
		answer := strings.TrimSpace(answers[i].Answer)
		if answer == "" && len(selected) > 0 {
			answer = strings.Join(selected, ", ")
		}
		if answer == "" {
			return "", ArgsError(fmt.Sprintf("ask: answer %d is empty", i+1))
		}
		if !q.Multiple {
			selected = nil
		}
		pairs = append(pairs, AskAnswer{Question: q.Question, Answer: answer, Selected: selected})
	}
	data, err := jsonx.Marshal(map[string]any{"answers": pairs})
	if err != nil {
		return "", ArgsError(fmt.Sprintf("ask: encode the result: %v", err))
	}
	return string(data), nil
}

func (t *AskTool) remove(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, key)
}

// Call registers the questions and blocks until the UI resolves them with
// Resolve, drops them with Cancel, or the turn stops. The session and call
// ids travel on the turn context (set by the agent and the registry).
func (t *AskTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	questions, err := parseAskQuestions(raw)
	if err != nil {
		return "", err
	}
	var sessionID, callID string
	var cancel context.Context
	if ctx != nil {
		sessionID, callID, cancel = ctx.SessionID, ctx.CallID, ctx.Cancel
	}
	if cancel == nil {
		cancel = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(callID) == "" {
		return "", ArgsError("ask: no session to ask in")
	}
	key := askKey(sessionID, callID)
	t.mu.Lock()
	if _, dup := t.pending[key]; dup {
		t.mu.Unlock()
		return "", ArgsError(fmt.Sprintf("ask: already waiting on an answer for call %q", callID))
	}
	wait := &askPending{questions: questions, done: make(chan askOutcome, 1)}
	t.pending[key] = wait
	t.mu.Unlock()

	select {
	case out := <-wait.done:
		t.remove(key)
		if out.cancelled {
			return "", ArgsError("ask: cancelled by the user")
		}
		return summarizeAsk(questions, out.answers)
	case <-cancel.Done():
		t.remove(key)
		return "", ArgsError("ask: the question was interrupted")
	}
}

// Resolve delivers the user's answers to the blocked ask call. Unknown
// calls are an error so the UI can tell the user the turn already moved on.
func (t *AskTool) Resolve(sessionID, callID string, answers []AskAnswer) error {
	t.mu.Lock()
	wait, ok := t.pending[askKey(sessionID, callID)]
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("ask: no pending question for call %q", callID)
	}
	select {
	case wait.done <- askOutcome{answers: answers}:
		return nil
	default:
		return fmt.Errorf("ask: no pending question for call %q", callID)
	}
}

// Cancel drops the blocked ask call; the caller also stops the turn so the
// agent does not continue headless. Reports whether anything was waiting.
// The entry is removed eagerly so only the first cancel reports true even
// when the blocked call has not woken up yet.
func (t *AskTool) Cancel(sessionID, callID string) bool {
	key := askKey(sessionID, callID)
	t.mu.Lock()
	wait, ok := t.pending[key]
	if ok {
		delete(t.pending, key)
	}
	t.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case wait.done <- askOutcome{cancelled: true}:
		return true
	default:
		return false
	}
}

// Pending reports whether an ask call is still waiting for an answer.
func (t *AskTool) Pending(sessionID, callID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.pending[askKey(sessionID, callID)]
	return ok
}
