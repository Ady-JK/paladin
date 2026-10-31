package main

// User terminals run with the user's normal OS permissions. They are
// separate from the agent's sandboxed shell tool and its transcript.
import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
)

type terminalEvent struct {
	Event      string `json:"event"`
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
	Data       string `json:"data,omitempty"` // base64: PTY bytes are not necessarily UTF-8
	ExitCode   *int   `json:"exitCode,omitempty"`
}

type terminalResult struct {
	Output    string `json:"output"`
	ExitCode  int    `json:"exitCode"`
	Truncated bool   `json:"truncated"`
}

type terminalProcess struct {
	sessionID string
	cmd       *exec.Cmd
	file      *os.File
}

type terminalHub struct {
	mu      sync.Mutex
	entries map[string]*terminalProcess
	emit    func(terminalEvent)
}

func newTerminalHub(emit func(terminalEvent)) *terminalHub {
	return &terminalHub{entries: make(map[string]*terminalProcess), emit: emit}
}

// Write PTY events directly to stdout. Unlike the notice queue, this
// provides backpressure if the renderer cannot keep up with terminal output.
var userTerminals = newTerminalHub(func(e terminalEvent) { emit(e) })

func terminalShell() string {
	if shell := os.Getenv("SHELL"); filepath.IsAbs(shell) {
		if info, err := os.Stat(shell); err == nil && !info.IsDir() {
			return shell
		}
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

func terminalDir(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("terminal needs an absolute workspace folder")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("terminal workspace is not a folder")
	}
	return dir, nil
}

func (h *terminalHub) start(sessionID, dir string, cols, rows uint16) (string, error) {
	if sessionID == "" {
		return "", errors.New("terminal needs a session")
	}
	var err error
	if dir, err = terminalDir(dir); err != nil {
		return "", err
	}
	if cols == 0 || rows == 0 || cols > 1000 || rows > 1000 {
		return "", errors.New("invalid terminal size")
	}
	if runtime.GOOS == "windows" {
		return "", errors.New("interactive terminal is not available on Windows")
	}
	h.mu.Lock()
	if len(h.entries) >= 32 {
		h.mu.Unlock()
		return "", errors.New("too many open terminals")
	}
	h.mu.Unlock()
	cmd := exec.Command(terminalShell(), "-il")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	h.mu.Lock()
	h.entries[id] = &terminalProcess{sessionID: sessionID, cmd: cmd, file: file}
	h.mu.Unlock()
	go func() {
		buffer := make([]byte, 16*1024)
		for {
			n, err := file.Read(buffer)
			if n > 0 {
				h.emit(terminalEvent{Event: "terminal/data", SessionID: sessionID, TerminalID: id, Data: base64.StdEncoding.EncodeToString(buffer[:n])})
			}
			if err != nil {
				break
			}
		}
		_ = file.Close()
		err := cmd.Wait()
		code := 0
		if err != nil {
			code = cmd.ProcessState.ExitCode()
		}
		h.mu.Lock()
		delete(h.entries, id)
		h.mu.Unlock()
		h.emit(terminalEvent{Event: "terminal/exit", SessionID: sessionID, TerminalID: id, ExitCode: &code})
	}()
	return id, nil
}

func (h *terminalHub) owned(sessionID, id string) (*terminalProcess, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.entries[id]
	if entry == nil || entry.sessionID != sessionID {
		return nil, errors.New("terminal not found for this session")
	}
	return entry, nil
}

func (h *terminalHub) write(sessionID, id, data string) error {
	entry, err := h.owned(sessionID, id)
	if err != nil {
		return err
	}
	if len(data) > 64*1024 {
		return errors.New("terminal input is too large")
	}
	_, err = io.WriteString(entry.file, data)
	return err
}

func (h *terminalHub) resize(sessionID, id string, cols, rows uint16) error {
	entry, err := h.owned(sessionID, id)
	if err != nil {
		return err
	}
	if cols == 0 || rows == 0 || cols > 1000 || rows > 1000 {
		return errors.New("invalid terminal size")
	}
	return pty.Setsize(entry.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (h *terminalHub) close(sessionID, id string) error {
	entry, err := h.owned(sessionID, id)
	if err != nil {
		return err
	}
	return killTerminalProcess(entry.cmd)
}

func (h *terminalHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, entry := range h.entries {
		_ = killTerminalProcess(entry.cmd)
	}
}

type limitedOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (w *limitedOutput) Write(chunk []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	const limit = 1024 * 1024
	if len(chunk) > limit-len(w.data) {
		w.truncated = true
	}
	if space := limit - len(w.data); space > 0 {
		w.data = append(w.data, chunk[:min(space, len(chunk))]...)
	}
	return len(chunk), nil
}

func terminalRun(command, dir string) (terminalResult, error) {
	if strings.TrimSpace(command) == "" {
		return terminalResult{}, errors.New("terminal command is empty")
	}
	var err error
	if dir, err = terminalDir(dir); err != nil {
		return terminalResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	shell := terminalShell()
	args := []string{"-lc", command}
	if runtime.GOOS == "windows" {
		args = []string{"/d", "/s", "/c", command}
	}
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 2 * time.Second
	output := &limitedOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	result := terminalResult{Output: string(output.data), Truncated: output.truncated}
	if ctx.Err() != nil {
		result.ExitCode = -1
		result.Output += "\n[command timed out after 10 minutes]"
		return result, nil
	}
	if err == nil {
		return result, nil
	}
	if exit := new(exec.ExitError); errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
}
