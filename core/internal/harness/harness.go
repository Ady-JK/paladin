// Package harness is the contract between Paladin's conversation and an external
// coding agent (today the Codex CLI). An agent speaks its own protocol; an
// adapter, one package per agent, translates what it does into the vocabulary
// Paladin's UI already draws: tool cards, questions, notices. The runtime owns the
// turn plumbing (scheduling, persistence, approvals) and asks the adapter only for
// the translation, so a new agent adds a package here and a UI module beside the
// Codex one, and shares the rest of the app, its colours and its cards.
//
// See docs/AGENT-ADAPTERS.md.
package harness

import (
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

// Card names the UI draws specially for any agent, whatever its own name for the
// thing.
const (
	// CardQuestion is a question the agent asked without stopping its work: an
	// inline form in the conversation, answered whenever the user likes.
	CardQuestion = "agent_question"
)

// Card is one agent action as a Paladin tool card: what it was and, once it has
// finished, how it went.
type Card struct {
	ID     string
	Name   string
	Args   string // JSON, the card's arguments
	Result string // JSON, set for a card built from an item that has already finished
}

// Question is one thing an agent asked: a prompt and the answers it suggests. The
// user may always answer in their own words as well.
type Question struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Multiple bool     `json:"multiple,omitempty"`
}

// Answer is the user's answer to one Question.
type Answer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	// Selected lists every chosen option of a multiple-choice question; Answer is
	// then those joined with ", ".
	Selected []string `json:"selected,omitempty"`
}

// Adapter is what the runtime needs from an agent's adapter beyond the
// event-by-event translation each adapter does inside its own package.
type Adapter interface {
	// Name is the harness name stored in the session ("codex").
	Name() string
	// QuestionReply is the user message that answers the questions a card asked: the
	// message the agent receives, and the short form the conversation shows.
	QuestionReply(callID string, questions []Question, answers []Answer) (message, display string)
	// Annotate settles, from the conversation alone, what a stored card could not
	// know when it was written (a question the user answered later). It returns a
	// copy; the stored messages are not touched.
	Annotate(msgs []transcript.Message) []transcript.Message
}
