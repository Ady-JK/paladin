// shell.go runs shell commands, sync or background.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/procstat"
	"github.com/Ady-JK/paladin/core/internal/redact"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

var (
	// DefaultShellYieldMs is how long a synchronous call waits before it
	// hands the still-running command to the background and returns a
	// job id. Nothing is killed: the command keeps running either way.
	// A variable so tests can shorten the wait.
	DefaultShellYieldMs = 10_000
)

const (
	MaxShellOutput       = 32 * 1024
	shellTruncatedMarker = "\n… [truncated]"

	ShellRun    = "run"
	ShellStatus = "status"
	ShellCancel = "cancel"

	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
	JobTimeout   = "timeout"

	MaxNotifyOutput = 50 * 1024
	NotifyTailLines = 2000
)

type ShellArgs struct {
	Action string `json:"action"`
	JobID  string `json:"job_id"`
	// Command is the model-facing name. Cmd is the old name, accepted so
	// calls saved or scripted before the rename keep working; Command wins
	// when both are present.
	Command    string `json:"command"`
	Cmd        string `json:"cmd"`
	Background bool   `json:"background"`
	Notify     bool   `json:"notify"`
}

// CommandLine returns the command to run, preferring command over the
// legacy cmd alias.
func (a ShellArgs) CommandLine() string {
	if strings.TrimSpace(a.Command) != "" {
		return a.Command
	}
	return a.Cmd
}

type ShellResponse struct {
	JobID     *string `json:"job_id,omitempty"`
	Status    string  `json:"status"`
	ExitCode  *int    `json:"exit_code,omitempty"`
	Output    *string `json:"output,omitempty"`
	Truncated *bool   `json:"truncated,omitempty"`
	// OutputFile holds the whole output when it outgrew the inline cap.
	OutputFile *string `json:"output_file,omitempty"`
	Log        *string `json:"log,omitempty"`
	// Note explains what happened when the answer is not the command's
	// own result (for example, that it was moved to the background).
	Note *string `json:"note,omitempty"`
}

// procTracker attributes shell children to their session for the
// resources RPC; tests swap it.
var procTracker = procstat.Default

type ShellTool struct {
	inner    *shellJobs
	observer JobObserver
}

func NewShellTool() *ShellTool {
	return &ShellTool{inner: &shellJobs{byID: map[string]*job{}, changed: newBroadcaster()}}
}

// runSync starts the command as a job and waits up to
// DefaultShellYieldMs for it. A command that outlives the wait keeps
// running: it is armed for a completion notice, gets a five-minute
// running notice until it ends, and the call returns its job id.
func (s *ShellTool) runSync(cmd, dir, owner, callID string, cancel context.Context, sessionDir string, sb *sandbox.Policy) (string, error) {
	cleanup := ""
	if sessionDir == "" {
		// No session (an eval, a plan check): keep the log in a temp
		// folder and take it away with the call.
		tmp, err := os.MkdirTemp("", "paladin-shell-")
		if err != nil {
			return "", ArgsError(fmt.Sprintf("shell: cannot keep the command output: %s", syserr.Str(err)))
		}
		sessionDir, cleanup = tmp, tmp
	}
	if cleanup != "" {
		defer os.RemoveAll(cleanup)
	}
	started, err := s.startJob(cmd, dir, owner, callID, false, true, sessionDir, sb)
	if err != nil {
		return "", err
	}
	select {
	case <-started.done:
		s.markReported(started.id)
		return s.completed(started.id, sb != nil)
	case <-time.After(time.Duration(DefaultShellYieldMs) * time.Millisecond):
		if s.armAuto(started.id) {
			return s.runningResult(started.id,
				fmt.Sprintf("still running after %dms; moved to the background. Its completion and its running time are reported to you automatically.", DefaultShellYieldMs))
		}
		s.markReported(started.id)
		return s.completed(started.id, sb != nil)
	case <-cancel.Done():
		_, _ = s.cancel(started.id, owner)
		select {
		case <-started.done:
		case <-time.After(2 * time.Second):
		}
		return "", ArgsError("shell: the command was interrupted")
	}
}

// completed builds the answer for a command that has finished.
func (s *ShellTool) completed(id string, sandboxed bool) (string, error) {
	out, terr := s.completedResult(id, sandboxed)
	if terr != nil {
		return "", terr
	}
	return out, nil
}

func (s *ShellTool) completedResult(id string, sandboxed bool) (string, *ToolError) {
	s.inner.mu.Lock()
	j, ok := s.inner.byID[id]
	var snap job
	if ok {
		snap = *j
	}
	s.inner.mu.Unlock()
	if !ok {
		return "", ArgsError(fmt.Sprintf("shell: unknown job %q", id))
	}
	if snap.status == JobCancelled {
		return "", ArgsError("shell: the command was cancelled")
	}
	raw, truncated, err := readJobOutput(snap.log, MaxShellOutput)
	if err != nil {
		return "", ArgsError(fmt.Sprintf("shell: cannot read the command output: %s", syserr.Str(err)))
	}
	outputFile := ""
	if truncated {
		outputFile = snap.log
	}
	raw = []byte(strings.ToValidUTF8(string(raw), "�"))
	return finishSync(raw, truncated, outputFile, waitResult{code: snap.exitCode, signaled: snap.exitCode == nil}, sandboxed)
}

func (s *ShellTool) runningResult(id, note string) (string, error) {
	s.inner.mu.Lock()
	j, ok := s.inner.byID[id]
	var snap job
	if ok {
		snap = *j
	}
	s.inner.mu.Unlock()
	if !ok {
		return "", ArgsError(fmt.Sprintf("shell: unknown job %q", id))
	}
	return shellResponseJSON(snap.id, JobRunning, nil, nil, nil, snap.log, "", note)
}

// readJobOutput reads at most max bytes of a command log: the whole
// file when it fits, otherwise its start and end around the truncation
// marker. The log itself keeps every byte.
func readJobOutput(path string, max int) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if st.Size() <= int64(max) {
		data, err := io.ReadAll(f)
		return data, false, err
	}
	half := max / 2
	head := make([]byte, half)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if _, err := f.Seek(st.Size()-int64(half), io.SeekStart); err != nil {
		return nil, false, err
	}
	tail := make([]byte, half)
	n, _ = io.ReadFull(f, tail)
	tail = tail[:n]
	out := make([]byte, 0, len(head)+len(shellTruncatedMarker)+len(tail))
	out = append(out, head...)
	out = append(out, shellTruncatedMarker...)
	out = append(out, tail...)
	return out, true, nil
}

func shellResponseJSON(id, status string, exitCode *int, output *string, truncated *bool, log, outputFile, note string) (string, error) {
	var logPtr, filePtr, notePtr *string
	if log != "" {
		logPtr = &log
	}
	if outputFile != "" {
		filePtr = &outputFile
	}
	if note != "" {
		notePtr = &note
	}
	if output != nil {
		masked := redact.String(*output)
		output = &masked
	}
	out := ShellResponse{Status: status, ExitCode: exitCode, Output: output, Truncated: truncated,
		OutputFile: filePtr, Log: logPtr, Note: notePtr}
	if id != "" {
		out.JobID = &id
	}
	data, err := jsonx.Marshal(out)
	if err != nil {
		return "", ArgsError(fmt.Sprintf("shell: encode the result: %v", err))
	}
	return string(data), nil
}

type waitResult struct {
	code     *int
	signaled bool
}

func finishSync(output []byte, truncated bool, outputFile string, w waitResult, sandboxed bool) (string, *ToolError) {
	if w.signaled || w.code == nil {
		return "", ArgsError("shell: the command exited with status 1")
	}
	code := *w.code
	text := ToLF(StripANSI(string(output)))
	if outputFile != "" {
		text = withSpillNote(text, outputFile)
	}
	var truncPtr *bool
	if truncated {
		truncPtr = &truncated
	}
	outText := text
	codePtr := code
	if code == 0 {
		text2, err := shellResponseJSON("", JobDone, &codePtr, &outText, truncPtr, "", outputFile, "")
		if err != nil {
			return "", mustToolError(err)
		}
		return text2, nil
	}
	content, err := shellResponseJSON("", JobFailed, &codePtr, &outText, truncPtr, "", outputFile, "")
	if err != nil {
		return "", mustToolError(err)
	}
	if sandboxed && sandbox.LooksBlocked(text) {
		content = markSandboxBlocked(content)
	}
	return "", WithContent(content, fmt.Sprintf("shell: the command exited with status %d", code))
}

// withSpillNote names the full output file at the truncation seam and
// gives up as much of the kept tail, so the inline output stays capped.
func withSpillNote(text, outputFile string) string {
	at := strings.Index(text, shellTruncatedMarker)
	if at < 0 {
		return text
	}
	note := " " + SpillNote(outputFile) + "\n"
	tail := text[at+len(shellTruncatedMarker):]
	cut := min(len(note), len(tail))
	for cut < len(tail) && !utf8.RuneStart(tail[cut]) {
		cut++
	}
	return text[:at] + shellTruncatedMarker + note + tail[cut:]
}

func mustToolError(err error) *ToolError {
	if terr := (*ToolError)(nil); errors.As(err, &terr) {
		return terr
	}
	return ArgsError(err.Error())
}

// ToLF strips ANSI escapes then normalizes all line endings to \n.
func ToLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(StripANSI(s), "\r\n", "\n"), "\r", "\n")
}

// StripANSI removes ESC [ ... final sequences (and a lone trailing
// ESC). Other bytes pass through untouched so UTF-8 stays intact.
func StripANSI(s string) string {
	b := []byte(s)
	var out []byte
	i := 0
	for i < len(b) {
		if b[i] != 0x1b {
			out = append(out, b[i])
			i++
			continue
		}
		i++
		if i >= len(b) {
			break
		}
		if b[i] == '[' {
			i++
			for i < len(b) && !(b[i] >= 0x40 && b[i] <= 0x7e) {
				i++
			}
			i++
		}
	}
	return string(out)
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

func (ShellTool) Name() string { return "shell" }

// Mutates marks the tool for undo snapshots.
func (ShellTool) Mutates() bool { return true }

func (ShellTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "shell",
		Description: "Run a shell command in the session workspace. " +
			"Pass the shell command in the command field. " +
			"There is no workdir: every run starts in the workspace root, " +
			"so cd inside the command when you need a subdirectory. " +
			"A normal call waits up to 10 seconds: a command that finishes sooner " +
			"returns its output, and one that runs longer is moved to the background " +
			"automatically and returns a job_id in running state. Nothing is killed. " +
			"Use background true to return at once for a long-running development server " +
			"or work that can proceed while other calls run. " +
			"Status checks a job, cancel stops one. " +
			"A job reports when it finishes and every five minutes while it runs; " +
			"keep working and do not poll it. Every command writes its whole output " +
			"to the log file named in the answer. " +
			"When a command exits, any processes it left behind are terminated; " +
			"start servers with background true rather than '&'.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":     map[string]any{"type": "string", "enum": []string{"run", "status", "cancel"}, "description": "What to do: run a command, check a job, or stop a job. Defaults to run."},
				"job_id":     map[string]any{"type": "string", "description": "The job to check or stop. Empty or missing for a new run."},
				"command":    map[string]any{"type": "string", "description": "The shell command to run. Required for action run. It starts in the session workspace; cd inside the command for a subdirectory."},
				"background": map[string]any{"type": "boolean", "description": "When true the call returns at once with a running job_id. Defaults to false."},
				"notify":     map[string]any{"type": "boolean", "description": "With background true, report the job's final state and output tail when it finishes. Defaults to false; ignored without background."},
			},
		},
	}
}

func (s *ShellTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	if ctx == nil {
		ctx = &ToolCtx{}
	}
	if ctx.Cancel == nil {
		ctx.Cancel = context.Background()
	}
	var args ShellArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("shell: the arguments are not valid JSON: %v", err))
	}
	switch args.Action {
	case "", ShellRun:
		cmd := args.CommandLine()
		if strings.TrimSpace(cmd) == "" {
			return "", ArgsError("shell: command is required")
		}
		dir := strings.TrimSpace(ctx.Workspace)
		if dir == "" {
			return "", ArgsError("shell: no workspace to run in")
		}
		info, statErr := os.Stat(dir)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return "", ArgsError(fmt.Sprintf("shell: workspace %q does not exist", dir))
			}
			return "", ArgsError(fmt.Sprintf("shell: workspace %q cannot be used: %s", dir, syserr.Str(statErr)))
		}
		if !info.IsDir() {
			return "", ArgsError(fmt.Sprintf("shell: workspace %q is not a directory", dir))
		}
		if args.Background {
			// Background runs detach; notify arming happens per-turn.
			return s.runBackground(cmd, dir, ctx.SessionID, ctx.CallID, args.Notify, ctx.SessionDir, ctx.Sandbox)
		}
		return s.runSync(cmd, dir, ctx.SessionID, ctx.CallID, ctx.Cancel, ctx.SessionDir, ctx.Sandbox)
	case ShellStatus:
		if strings.TrimSpace(args.JobID) == "" {
			return "", ArgsError("shell: job_id is required for this action")
		}
		return s.status(args.JobID, ctx.SessionID)
	case ShellCancel:
		if strings.TrimSpace(args.JobID) == "" {
			return "", ArgsError("shell: job_id is required for this action")
		}
		return s.cancel(args.JobID, ctx.SessionID)
	default:
		return "", ArgsError(fmt.Sprintf("shell: action must be one of run, status or cancel, got %q", args.Action))
	}
}

// sandboxBlockedHint tells the model why a write failed under the sandbox.
const sandboxBlockedHint = "the OS sandbox blocked a write outside the workspace; the user was asked whether to rerun it unsandboxed"

// markSandboxBlocked flags a failed result so the agent can offer to
// rerun the command outside the sandbox.
func markSandboxBlocked(content string) string {
	var m map[string]any
	if json.Unmarshal([]byte(content), &m) != nil {
		return content
	}
	m["sandbox_blocked"] = true
	m["hint"] = sandboxBlockedHint
	b, err := json.Marshal(m)
	if err != nil {
		return content
	}
	return string(b)
}

// SandboxBlocked reports whether a shell result was flagged by
// markSandboxBlocked, and the command's output.
func SandboxBlocked(content string) (output string, blocked bool) {
	var m struct {
		Blocked bool   `json:"sandbox_blocked"`
		Output  string `json:"output"`
	}
	if json.Unmarshal([]byte(content), &m) != nil || !m.Blocked {
		return "", false
	}
	return m.Output, true
}

// confine rewrites cmd to run inside the sandbox policy (nil: as is).
func confine(cmd *exec.Cmd, sb *sandbox.Policy) error {
	if sb == nil || sb.Mode == sandbox.ModeNone {
		return nil
	}
	inner := append([]string{cmd.Path}, cmd.Args[1:]...)
	argv, err := sandbox.Wrap(*sb, inner)
	if err != nil {
		return err
	}
	cmd.Path, cmd.Args = argv[0], argv
	return nil
}
