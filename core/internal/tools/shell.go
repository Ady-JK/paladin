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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/procstat"
	"github.com/Ady-JK/paladin/core/internal/redact"
	"github.com/Ady-JK/paladin/core/internal/sandbox"
	"github.com/Ady-JK/paladin/core/internal/syserr"
)

const (
	DefaultShellTimeoutMs = 10_000
	// DefaultJobTimeoutMs is the default for background runs, which
	// are meant for long work.
	DefaultJobTimeoutMs  = 300_000
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
	TimeoutMs  int64  `json:"timeout_ms"`
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

const maxShellTimeoutMs = int64(^uint64(0)>>1) / int64(time.Millisecond)

func shellTimeoutMs(ms int64, background bool) (time.Duration, error) {
	if ms == 0 && background {
		return DefaultJobTimeoutMs * time.Millisecond, nil
	}
	if ms == 0 {
		return DefaultShellTimeoutMs * time.Millisecond, nil
	}
	if ms < 0 {
		return 0, fmt.Errorf("shell: timeout_ms cannot be negative")
	}
	if ms > maxShellTimeoutMs {
		return 0, fmt.Errorf("shell: timeout_ms exceeds the maximum supported value of %d", maxShellTimeoutMs)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (s *ShellTool) runSync(cmd, dir, owner, spillPath string, limit time.Duration, cancel context.Context, sb *sandbox.Policy) (string, error) {
	out, terr := runSyncInner(cmd, dir, owner, spillPath, limit, cancel, sb)
	if terr != nil {
		return "", terr
	}
	return out, nil
}

func shellResponseJSON(id, status string, exitCode *int, output *string, truncated *bool, log, outputFile string) (string, error) {
	var logPtr, filePtr *string
	if log != "" {
		logPtr = &log
	}
	if outputFile != "" {
		filePtr = &outputFile
	}
	if output != nil {
		masked := redact.String(*output)
		output = &masked
	}
	out := ShellResponse{Status: status, ExitCode: exitCode, Output: output, Truncated: truncated, OutputFile: filePtr, Log: logPtr}
	if id != "" {
		out.JobID = &id
	}
	data, err := jsonx.Marshal(out)
	if err != nil {
		return "", ArgsError(fmt.Sprintf("shell: encode the result: %v", err))
	}
	return string(data), nil
}

func runSyncInner(cmdStr, dir, owner, spillPath string, limit time.Duration, cancel context.Context, sb *sandbox.Policy) (string, *ToolError) {
	child, outR, errR, err := spawnPiped(cmdStr, dir, sb)
	if err != nil {
		return "", ArgsError(fmt.Sprintf("shell: cannot start the command: %s", syserr.Str(err)))
	}
	pid := 0
	if child.Process != nil {
		pid = child.Process.Pid
	}
	defer procTracker.Track(owner, pid)()
	capture := newShellOutputCapture(MaxShellOutput)
	if spillPath != "" {
		capture.spillTo(spillPath)
	}
	defer capture.finishSpill()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); _, _ = io.Copy(capture, outR) }()
	go func() { defer readers.Done(); _, _ = io.Copy(capture, errR) }()
	ioDone := make(chan struct{})
	go func() { readers.Wait(); close(ioDone) }()
	// Closing our read ends ends the copies; a background grandchild that
	// still holds the pipes cannot keep the call waiting past this.
	defer func() { _ = outR.Close(); _ = errR.Close() }()
	waitDone := make(chan waitResult, 1)
	go func() {
		werr := child.Wait()
		code, signaled := exitCode(werr)
		waitDone <- waitResult{code: code, signaled: signaled}
	}()
	start := time.Now()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-cancel.Done():
			killProcessGroup(pid)
			<-waitDone
			<-ioDone
			return "", ArgsError("shell: the command was interrupted")
		case <-ticker.C:
			if time.Since(start) >= limit {
				killProcessGroup(pid)
				<-waitDone
				<-ioDone
				return "", ArgsError(fmt.Sprintf("shell: the command ran past its timeout of %dms", limit.Milliseconds()))
			}
		case w := <-waitDone:
			select {
			case <-ioDone:
			case <-time.After(10 * time.Second):
			}
			output, truncated := capture.result()
			return finishSync(output, truncated, capture.finishSpill(), w, sb != nil)
		}
	}
}

type waitResult struct {
	code     *int
	signaled bool
}

type shellOutputCapture struct {
	mu       sync.Mutex
	maxBytes int
	head     []byte
	tail     []byte
	total    int64
	// The spill keeps every byte once output outgrows maxBytes (spill.go).
	spillPath   string
	spill       *os.File
	spilled     int64
	spillFailed bool
}

func newShellOutputCapture(maxBytes int) *shellOutputCapture {
	half := maxBytes / 2
	return &shellOutputCapture{maxBytes: maxBytes, head: make([]byte, 0, half), tail: make([]byte, 0, maxBytes-half)}
}

func (c *shellOutputCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	c.spillLocked(p)
	c.total += int64(n)
	headCap := c.maxBytes / 2
	if len(c.head) < headCap {
		add := headCap - len(c.head)
		if add > len(p) {
			add = len(p)
		}
		c.head = append(c.head, p[:add]...)
		p = p[add:]
	}
	if len(p) > 0 {
		tailCap := c.maxBytes - headCap
		if len(p) >= tailCap {
			c.tail = append(c.tail[:0], p[len(p)-tailCap:]...)
		} else {
			over := len(c.tail) + len(p) - tailCap
			if over > 0 {
				copy(c.tail, c.tail[over:])
				c.tail = c.tail[:len(c.tail)-over]
			}
			c.tail = append(c.tail, p...)
		}
	}
	return n, nil
}

func (c *shellOutputCapture) result() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	truncated := c.total > int64(c.maxBytes)
	if truncated {
		// Each half is cleaned on its own so a rune split at either cut
		// never corrupts the text; the marker sits at the seam.
		head := strings.ToValidUTF8(string(c.head), "�")
		tail := strings.ToValidUTF8(string(c.tail), "�")
		return []byte(head + shellTruncatedMarker + tail), true
	}
	out := make([]byte, 0, len(c.head)+len(c.tail))
	out = append(out, c.head...)
	out = append(out, c.tail...)
	return []byte(strings.ToValidUTF8(string(out), "�")), false
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
		text2, err := shellResponseJSON("", JobDone, &codePtr, &outText, truncPtr, "", outputFile)
		if err != nil {
			return "", mustToolError(err)
		}
		return text2, nil
	}
	content, err := shellResponseJSON("", JobFailed, &codePtr, &outText, truncPtr, "", outputFile)
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
			"With background false (the default) the call waits for the command and " +
			"returns its output; with background true it returns at once with a " +
			"job_id in running state and writes everything to a log file. " +
			"Status checks a background job, cancel stops one. Prefer " +
			"synchronous runs for finite commands, raising timeout_ms for slow ones. " +
			"Use background for a long-running development server or work that " +
			"can proceed while other calls run. Check status and read the log as " +
			"needed, or set notify true to receive the final state later.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":     map[string]any{"type": "string", "enum": []string{"run", "status", "cancel"}, "description": "What to do: run a command, check a job, or stop a job. Defaults to run."},
				"job_id":     map[string]any{"type": "string", "description": "The job to check or stop. Empty or missing for a new run."},
				"command":    map[string]any{"type": "string", "description": "The shell command to run. Required for action run. It starts in the session workspace; cd inside the command for a subdirectory."},
				"background": map[string]any{"type": "boolean", "description": "When true the call returns at once with a running job_id. Defaults to false."},
				"notify":     map[string]any{"type": "boolean", "description": "With background true, remind the agent when the job finishes: its final state and output tail arrive on their own in a later round. Defaults to false; ignored without background."},
				"timeout_ms": map[string]any{"type": "integer", "description": "How long a command may run, in milliseconds. Defaults to 10000, or 300000 with background.", "minimum": 1, "maximum": maxShellTimeoutMs},
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
		limit, err := shellTimeoutMs(args.TimeoutMs, args.Background)
		if err != nil {
			return "", ArgsError(err.Error())
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
			return s.runBackground(cmd, dir, ctx.SessionID, ctx.CallID, limit, args.Notify, ctx.SessionDir, ctx.Sandbox)
		}
		return s.runSync(cmd, dir, ctx.SessionID, SpillPath(ctx), limit, ctx.Cancel, ctx.Sandbox)
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
