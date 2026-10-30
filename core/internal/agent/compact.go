// compact.go keeps long sessions inside the model's
// context window. When the prompt nears the window (proactive) or the
// provider rejects it as too long (reactive), the older part of the
// conversation is summarized by the model and replaced with that
// summary; the recent tail is kept verbatim. The transcript stays
// append-only: a compaction marker carries the summary and the kept
// tail is re-recorded after it as replay copies, so the model view can
// be rebuilt from the last marker after a restart.

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/hooks"
	"github.com/Ady-JK/paladin/core/internal/inference"
	"github.com/Ady-JK/paladin/core/internal/transcript"
)

const (
	// compactThresholdPct is the window fill that triggers proactive
	// compaction before the next round.
	compactThresholdPct = 85
	// maxUnproductiveCompactions bounds back-to-back compactions that
	// did not make room (failed, or the context is still over the
	// threshold). A turn has no round cap and may compact any number of
	// times, but a prompt that can't shrink stops compacting, and an
	// overflow then fails the turn instead of looping.
	maxUnproductiveCompactions = 3
	// summaryToolResultChars caps each tool result fed to the summarizer.
	summaryToolResultChars = 2000
	// summaryArgChars caps each tool call's arguments in the summary input.
	summaryArgChars = 500
	// summaryInputTokensUnknown bounds the summary input when the model's
	// window is unknown.
	summaryInputTokensUnknown = 60000
)

const compactSystemPrompt = `You are compacting the conversation of an autonomous coding agent that has outgrown the model's context window. Write a summary that lets the agent continue the work seamlessly without the original messages.

Include, as applicable:
1. The user's goals and every explicit instruction, preference and constraint (quote them when precise wording matters).
2. Work completed so far and key decisions with their reasons.
3. Files read, created or modified, with the details that matter (paths, functions, important snippets, commands run).
4. Errors encountered and how they were resolved, or whether they are still open.
5. The current state: what was in progress at the end of the conversation.
6. Remaining next steps.

Be specific and factual; never invent details. Output only the summary.`

// CompactionStatus tells the UI a context compaction is running or
// finished. Phase is "start", "done" or "failed".
type CompactionStatus struct {
	Phase        string `json:"phase"`
	Reason       string `json:"reason"`
	TokensBefore int64  `json:"tokensBefore"`
	TokensAfter  int64  `json:"tokensAfter,omitempty"`
	Error        string `json:"error,omitempty"`
}

var errNothingToCompact = errors.New("nothing older than the current exchange to compact")

// compactionUserContent is the model-facing stand-in for everything
// before a compaction marker.
func compactionUserContent(summary string) string {
	return "This session was compacted to fit the model's context window. Summary of the earlier conversation:\n\n" +
		summary +
		"\n\n(The conversation continues below. Re-read files before relying on exact contents.)"
}

// estimateTokens is a cheap chars/4 estimate, used only where no
// provider-reported count exists.
func estimateTokens(s string) int64 { return int64(len(s)+3) / 4 }

func estimateMessageTokens(m inference.LlmMessage) int64 {
	n := estimateTokens(m.Content) + 4
	for _, c := range m.ToolCalls {
		n += estimateTokens(c.Name) + estimateTokens(c.Arguments) + 4
	}
	return n
}

func estimateMessagesTokens(msgs []inference.LlmMessage) int64 {
	var n int64
	for _, m := range msgs {
		n += estimateMessageTokens(m)
	}
	return n
}

// contextTokens is the full prompt size a usage report describes.
// Anthropic reports cached input separately from input_tokens; the
// OpenAI-style APIs fold it into prompt_tokens.
func contextTokens(u inference.Usage, api config.ApiType) int64 {
	if api == config.ApiTypeAnthropic {
		return u.PromptTokens + u.CacheReadTokens + u.CacheWriteTokens
	}
	return u.PromptTokens
}

// estimateContext is the size of the next request: the last reported
// prompt plus an estimate of what was appended since, or a full
// estimate when no report applies to the current message list.
func (a *Agent) estimateContext() int64 {
	if a.promptTokens > 0 && a.promptLen <= len(a.messages) {
		return a.promptTokens + estimateMessagesTokens(a.messages[a.promptLen:])
	}
	n := estimateTokens(a.system) + estimateMessagesTokens(a.messages)
	for _, d := range a.toolDefs {
		params, _ := json.Marshal(d.Parameters)
		n += estimateTokens(d.Name) + estimateTokens(d.Description) + estimateTokens(string(params))
	}
	return n
}

// splitForCompaction returns the index where the kept tail starts:
// the newest messages fitting in keep tokens, moved forward so a tool
// result never starts the tail without its call. When not even the
// last message fits, the whole last block is kept. 0 means there is
// nothing older to summarize.
func splitForCompaction(msgs []inference.LlmMessage, keep int64) int {
	n := len(msgs)
	cut := n
	var acc int64
	for i := n - 1; i >= 0; i-- {
		t := estimateMessageTokens(msgs[i])
		if acc+t > keep {
			break
		}
		acc += t
		cut = i
	}
	if cut == 0 {
		return 0
	}
	for cut < n && msgs[cut].Role == "tool" {
		cut++
	}
	if cut == n {
		cut = n - 1
		for cut > 0 && msgs[cut].Role == "tool" {
			cut--
		}
	}
	return cut
}

// renderForSummary flattens messages into a plain-text transcript, so
// the summary call needs no tool-call wire format. Oversized input
// keeps the opening (usually the task) and the most recent part.
func renderForSummary(msgs []inference.LlmMessage, maxChars int) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			b.WriteString("## User\n")
			b.WriteString(m.Content)
		case "assistant":
			b.WriteString("## Assistant\n")
			b.WriteString(m.Content)
			for _, c := range m.ToolCalls {
				fmt.Fprintf(&b, "\n[called tool %s %s]", c.Name, clip(c.Arguments, summaryArgChars))
			}
		case "tool":
			name := "tool"
			if m.Name != nil && *m.Name != "" {
				name = *m.Name
			}
			fmt.Fprintf(&b, "## Tool result (%s)\n", name)
			b.WriteString(clip(m.Content, summaryToolResultChars))
		default:
			continue
		}
		b.WriteString("\n\n")
	}
	s := b.String()
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	head := maxChars / 5
	tail := maxChars - head
	return validUTF8(s[:head]) + "\n\n[… earlier conversation omitted …]\n\n" + validUTF8(s[len(s)-tail:])
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return validUTF8(s[:n]) + fmt.Sprintf("… [%d more chars]", len(s)-n)
}

// validUTF8 drops a rune split by a byte-offset cut.
func validUTF8(s string) string { return strings.ToValidUTF8(s, "") }

// replayRecord converts a kept model message back into a transcript
// record flagged as a replay copy.
func replayRecord(m inference.LlmMessage, now time.Time) transcript.Message {
	rec := transcript.Message{Content: m.Content, CreatedAt: now, Replay: true}
	switch m.Role {
	case "assistant":
		rec.Role = transcript.RoleAssistant
		if len(m.ToolCalls) > 0 {
			calls := make([]transcript.ToolCall, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				calls = append(calls, transcript.ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments, Signature: c.Signature})
			}
			rec.ToolCalls = &calls
		}
	case "tool":
		rec.Role = transcript.RoleTool
		rec.ToolCallID = m.ToolCallID
		rec.Name = m.Name
	default:
		rec.Role = transcript.RoleUser
	}
	return rec
}

// maybeCompact compacts before a round when the next request would
// fill most of a known context window. Failure is not fatal: the round
// runs uncompacted and a real overflow still gets the reactive path.
func (a *Agent) maybeCompact() {
	if a.window <= 0 || a.compactStreak >= maxUnproductiveCompactions {
		return
	}
	est := a.estimateContext()
	if !a.overThreshold(est) {
		return
	}
	_ = a.compact("auto", est)
}

// overThreshold reports whether est fills most of a known window.
func (a *Agent) overThreshold(est int64) bool {
	return a.window > 0 && est*100 >= a.window*compactThresholdPct
}

// noteRoundSucceeded ends a compaction streak once a round got through
// with the context back under the threshold.
func (a *Agent) noteRoundSucceeded() {
	if !a.overThreshold(a.estimateContext()) {
		a.compactStreak = 0
	}
}

// recoverRound decides whether a failed round runs again: a context
// overflow is answered by compacting, anything else by retryRound.
func (a *Agent) recoverRound(err error, noProgress bool) bool {
	if inference.IsContextOverflow(err) {
		if !noProgress || a.compactStreak >= maxUnproductiveCompactions {
			return false
		}
		if a.compact("overflow", a.estimateContext()) != nil {
			return false
		}
		if a.round > 0 {
			a.round--
		}
		return true
	}
	return a.retryRound(err, noProgress)
}

// roundFailure is the turn error for an unrecovered provider failure.
func roundFailure(err error) turnFailed {
	if inference.IsContextOverflow(err) {
		return turnFailed{"context window exceeded (compaction could not make room): " + err.Error()}
	}
	return turnFailed{"provider unavailable: " + err.Error()}
}

// compact summarizes the older part of a.messages and swaps it for the
// summary. On success the marker and replay copies join a.records.
func (a *Agent) compact(reason string, before int64) error {
	a.compactStreak++
	// Keep a quarter of the conversation itself (the system prompt and
	// tool schemas are fixed overhead), at most a quarter of the window.
	keep := estimateMessagesTokens(a.messages) / 4
	if a.window > 0 && a.window/4 < keep {
		keep = a.window / 4
	}
	cut := splitForCompaction(a.messages, keep)
	if cut == 0 {
		return errNothingToCompact
	}
	trigger := "auto"
	if reason == "manual" {
		trigger = "manual"
	}
	pre := a.hookInput(hooks.PreCompact)
	pre.Trigger = trigger
	preOut := a.runHooks(pre)
	if preOut.Blocked || preOut.Stop {
		why := preOut.Reason
		if why == "" {
			why = preOut.StopReason
		}
		return fmt.Errorf("blocked by a PreCompact hook: %s", why)
	}
	_ = a.emit(ChatChunk{Compaction: &CompactionStatus{Phase: "start", Reason: reason, TokensBefore: before}})
	budget := int64(summaryInputTokensUnknown)
	if a.window > 0 {
		budget = a.window / 2
	}
	prompt := "Summarize the conversation below.\n\n<conversation>\n" +
		renderForSummary(a.messages[:cut], int(budget*4)) +
		"</conversation>"
	if len(preOut.Context) > 0 {
		prompt += "\n\nAdditional instructions for the summary:\n" + strings.Join(preOut.Context, "\n")
	}
	var resp inference.ToolResponse
	err := guardProvider(func() error {
		var cerr error
		resp, cerr = a.provider.CompleteTools(a.ctx, compactSystemPrompt,
			[]inference.LlmMessage{{Role: "user", Content: prompt}}, nil, a.session.Meta.SessionID)
		return cerr
	})
	if err == nil && resp.Usage != nil {
		a.noteCallUsage(*resp.Usage)
	}
	summary := strings.TrimSpace(resp.Content)
	if err == nil && summary == "" {
		err = errors.New("model returned an empty summary")
	}
	if err != nil {
		_ = a.emit(ChatChunk{Compaction: &CompactionStatus{Phase: "failed", Reason: reason, TokensBefore: before, Error: err.Error()}})
		return err
	}
	if a.hooks.Has(hooks.SessionStart) {
		start := a.hookInput(hooks.SessionStart)
		start.Source = "compact"
		summary += hookContext(hooks.SessionStart, a.runHooks(start).Context)
	}
	now := time.Now().UTC()
	tail := a.messages[cut:]
	a.records = append(a.records, transcript.Message{
		Role: transcript.RoleSystem, Content: summary, CreatedAt: now,
		Compaction: &transcript.Compaction{TokensBefore: before, Reason: reason},
	})
	for _, m := range tail {
		a.records = append(a.records, replayRecord(m, now))
	}
	msgs := make([]inference.LlmMessage, 0, len(tail)+1)
	msgs = append(msgs, inference.LlmMessage{Role: "user", Content: compactionUserContent(summary)})
	a.messages = append(msgs, tail...)
	a.promptTokens, a.promptLen = 0, 0
	_ = a.emit(ChatChunk{Compaction: &CompactionStatus{Phase: "done", Reason: reason, TokensBefore: before, TokensAfter: a.estimateContext()}})
	post := a.hookInput(hooks.PostCompact)
	post.Trigger = trigger
	a.runHooks(post)
	return nil
}

// runCompact is a manual /compact turn: summarize everything except a
// short tail and persist the marker, with no model round and no user
// message. An empty or tiny session reports errNothingToCompact.
func (a *Agent) runCompact() (ChatOutcome, error) {
	a.currentResume = true
	if err := a.compact("manual", a.estimateContext()); err != nil {
		if errors.Is(err, errNothingToCompact) {
			return ChatOutcome{}, turnFailed{"nothing to compact yet: the conversation is too short"}
		}
		return ChatOutcome{}, turnFailed{"compaction failed: " + err.Error()}
	}
	a.finish(phaseDone)
	if err := a.save(); err != nil {
		return ChatOutcome{}, err
	}
	return ChatOutcome{SessionID: a.session.Meta.SessionID, Usage: a.turnUsage}, nil
}
