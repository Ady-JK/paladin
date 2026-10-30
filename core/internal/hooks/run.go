package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout = 60 * time.Second
	// maxOutput caps what one hook may write to stdout or stderr; the
	// rest is dropped so a chatty script cannot flood the model.
	maxOutput = 64 << 10
)

type Decision string

const (
	DecisionNone  Decision = ""
	DecisionAllow Decision = "allow"
	DecisionAsk   Decision = "ask"
	DecisionDeny  Decision = "deny"
)

func (d Decision) rank() int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	}
	return 0
}

// Note outcomes, shown to the user as a quiet row in the turn.
const (
	NoteBlocked = "blocked"
	NoteContext = "context"
	NoteError   = "error"
	NoteStopped = "stopped"
	NoteMessage = "message"
)

// Note reports one hook run that did something the user should see.
type Note struct {
	Event   Event  `json:"event"`
	Command string `json:"command"`
	Outcome string `json:"outcome"`
	Message string `json:"message,omitempty"`
}

// Outcome merges every handler that ran for one event.
type Outcome struct {
	// Blocked: the hook vetoed the action. Reason is fed to the model.
	Blocked bool
	Reason  string
	// Decision is PreToolUse's permission verdict (deny > ask > allow).
	Decision     Decision
	UpdatedInput json.RawMessage
	// Context is extra text for the model.
	Context []string
	// Messages are systemMessage strings meant for the user.
	Messages []string
	// Stop: a hook returned continue:false; the turn should end.
	Stop       bool
	StopReason string
	Notes      []Note
}

type activeGroup struct {
	matcher  matcher
	handlers []Handler
}

// Engine runs the active (trusted, enabled) hooks for one folder. A nil
// Engine has no hooks, so callers need no nil checks.
type Engine struct {
	groups map[Event][]activeGroup
}

// Has reports whether any handler is configured for ev.
func (e *Engine) Has(ev Event) bool {
	return e != nil && len(e.groups[ev]) > 0
}

// subject is what an event's matcher is tested against; nil means the
// event ignores matchers.
func subject(in Input) []string {
	switch in.Event {
	case PreToolUse, PostToolUse:
		return toolNames(in.ToolName)
	case SessionStart:
		return []string{in.Source}
	case PreCompact, PostCompact:
		return []string{in.Trigger}
	}
	return nil
}

func (e *Engine) selected(in Input) []Handler {
	if e == nil {
		return nil
	}
	names := subject(in)
	seen := map[string]bool{}
	var out []Handler
	for _, g := range e.groups[in.Event] {
		if names != nil && !g.matcher.matches(names) {
			continue
		}
		for _, h := range g.handlers {
			cmd := platformCommand(h)
			if cmd == "" || seen[cmd] {
				continue
			}
			seen[cmd] = true
			out = append(out, h)
		}
	}
	return out
}

// Run executes every matching handler in parallel and merges results
// in config order. Hook failures never fail the caller: they become
// error notes.
func (e *Engine) Run(ctx context.Context, in Input) Outcome {
	handlers := e.selected(in)
	if len(handlers) == 0 {
		return Outcome{}
	}
	stdin, err := json.Marshal(in)
	if err != nil {
		return Outcome{Notes: []Note{{Event: in.Event, Outcome: NoteError, Message: err.Error()}}}
	}
	results := make([]effect, len(handlers))
	var wg sync.WaitGroup
	for i, h := range handlers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := execute(ctx, h, stdin, in.Cwd)
			results[i] = interpret(in.Event, res)
			results[i].note.Event = in.Event
			results[i].note.Command = platformCommand(h)
		}()
	}
	wg.Wait()
	return merge(results)
}

type runResult struct {
	stdout, stderr string
	code           int
	err            error
}

// execute runs one handler with stdin, in cwd, under its timeout. On
// timeout or cancel the whole process group is killed.
func execute(ctx context.Context, h Handler, stdin []byte, cwd string) runResult {
	timeout := defaultTimeout
	if h.Timeout > 0 {
		timeout = time.Duration(h.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellCommand(ctx, platformCommand(h))
	if cwd != "" {
		if st, err := os.Stat(cwd); err == nil && st.IsDir() {
			cmd.Dir = cwd
		}
	}
	cmd.Env = append(os.Environ(), "PALADIN_PROJECT_DIR="+cwd, "CLAUDE_PROJECT_DIR="+cwd)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr cappedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	setProcessGroup(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	cmd.WaitDelay = 2 * time.Second

	err := cmd.Run()
	res := runResult{stdout: stdout.String(), stderr: stderr.String()}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.err = fmt.Errorf("timed out after %s", timeout)
	case ctx.Err() != nil:
		res.err = errors.New("cancelled")
	case err != nil:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			res.code = exit.ExitCode()
		} else {
			res.err = err
		}
	}
	return res
}

func platformCommand(h Handler) string {
	if runtime.GOOS == "windows" && strings.TrimSpace(h.CommandWindows) != "" {
		return h.CommandWindows
	}
	return h.Command
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

// cappedBuffer keeps the first maxOutput bytes and drops the rest. The
// buffer is a named field so io.Copy cannot bypass Write via ReadFrom.
type cappedBuffer struct{ buf bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }

// effect is one handler's parsed result.
type effect struct {
	blocked    bool
	reason     string
	decision   Decision
	updated    json.RawMessage
	context    string
	message    string
	stop       bool
	stopReason string
	note       Note
	noted      bool
}

// canBlock: events where exit 2 / decision:block vetoes something.
// SessionStart and PostCompact have nothing to veto.
func canBlock(ev Event) bool {
	switch ev {
	case PreToolUse, PostToolUse, UserPromptSubmit, Stop, PreCompact:
		return true
	}
	return false
}

// plainContext: events whose plain (non-JSON) stdout is model context.
func plainContext(ev Event) bool {
	return ev == UserPromptSubmit || ev == SessionStart
}

func (f *effect) mark(outcome, msg string) {
	f.note.Outcome, f.note.Message, f.noted = outcome, msg, true
}

func interpret(ev Event, r runResult) effect {
	var f effect
	stderr := strings.TrimSpace(r.stderr)
	switch {
	case r.err != nil:
		f.mark(NoteError, r.err.Error())
		return f
	case r.code == 2:
		if stderr == "" {
			f.mark(NoteError, "exited with code 2 but wrote no reason to stderr")
		} else if canBlock(ev) {
			f.blocked, f.reason = true, stderr
			f.mark(NoteBlocked, stderr)
		} else {
			f.mark(NoteMessage, stderr)
		}
		return f
	case r.code != 0:
		msg := fmt.Sprintf("exited with code %d", r.code)
		if stderr != "" {
			msg += ": " + stderr
		}
		f.mark(NoteError, msg)
		return f
	}
	out := strings.TrimSpace(r.stdout)
	if out == "" {
		return f
	}
	if !strings.HasPrefix(out, "{") {
		if plainContext(ev) {
			f.context = out
			f.mark(NoteContext, out)
		}
		return f
	}
	parseJSON(ev, out, &f)
	return f
}

type wireOutput struct {
	Continue      *bool  `json:"continue"`
	StopReason    string `json:"stopReason"`
	SystemMessage string `json:"systemMessage"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	Specific      *struct {
		AdditionalContext        string          `json:"additionalContext"`
		PermissionDecision       string          `json:"permissionDecision"`
		PermissionDecisionReason string          `json:"permissionDecisionReason"`
		UpdatedInput             json.RawMessage `json:"updatedInput"`
	} `json:"hookSpecificOutput"`
}

func parseJSON(ev Event, out string, f *effect) {
	var w wireOutput
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		f.mark(NoteError, "invalid JSON output: "+err.Error())
		return
	}
	if w.SystemMessage != "" {
		f.message = w.SystemMessage
		f.mark(NoteMessage, w.SystemMessage)
	}
	if w.Specific != nil && strings.TrimSpace(w.Specific.AdditionalContext) != "" {
		f.context = strings.TrimSpace(w.Specific.AdditionalContext)
		f.mark(NoteContext, f.context)
	}
	if ev == PreToolUse && w.Specific != nil && w.Specific.PermissionDecision != "" {
		switch d := Decision(w.Specific.PermissionDecision); d {
		case DecisionDeny:
			f.decision, f.blocked = d, true
			f.reason = strings.TrimSpace(w.Specific.PermissionDecisionReason)
			if f.reason == "" {
				f.reason = "denied by hook"
			}
			f.mark(NoteBlocked, f.reason)
		case DecisionAsk, DecisionAllow:
			f.decision = d
			f.reason = strings.TrimSpace(w.Specific.PermissionDecisionReason)
			if len(w.Specific.UpdatedInput) > 0 && string(w.Specific.UpdatedInput) != "null" {
				f.updated = w.Specific.UpdatedInput
			}
		default:
			f.mark(NoteError, fmt.Sprintf("unknown permissionDecision %q", d))
		}
	} else if w.Decision != "" {
		switch w.Decision {
		case "block":
			reason := strings.TrimSpace(w.Reason)
			switch {
			case reason == "":
				f.mark(NoteError, `decision "block" needs a reason`)
			case canBlock(ev):
				f.blocked, f.reason = true, reason
				if ev == PreToolUse {
					f.decision = DecisionDeny
				}
				f.mark(NoteBlocked, reason)
			}
		case "approve":
			if ev == PreToolUse {
				f.decision = DecisionAllow
			}
		default:
			f.mark(NoteError, fmt.Sprintf("unknown decision %q", w.Decision))
		}
	}
	if w.Continue != nil && !*w.Continue {
		f.stop, f.stopReason = true, strings.TrimSpace(w.StopReason)
		f.mark(NoteStopped, f.stopReason)
	}
}

func merge(results []effect) Outcome {
	var out Outcome
	var reasons []string
	for _, f := range results {
		if f.blocked {
			out.Blocked = true
			reasons = append(reasons, f.reason)
		}
		if f.decision.rank() > out.Decision.rank() {
			out.Decision = f.decision
		}
		if f.updated != nil && out.UpdatedInput == nil {
			out.UpdatedInput = f.updated
		}
		if f.context != "" {
			out.Context = append(out.Context, f.context)
		}
		if f.message != "" {
			out.Messages = append(out.Messages, f.message)
		}
		if f.stop {
			if !out.Stop {
				out.StopReason = f.stopReason
			}
			out.Stop = true
		}
		if f.noted {
			out.Notes = append(out.Notes, f.note)
		}
	}
	out.Reason = strings.Join(reasons, "\n")
	if out.Decision == DecisionDeny {
		out.UpdatedInput = nil
	}
	return out
}
