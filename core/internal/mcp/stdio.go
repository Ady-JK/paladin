package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// maxStdioMessage caps one newline-delimited message from a server.
const maxStdioMessage = 64 << 20

// stdioTransport runs the server as a child in its own process group
// and speaks newline-delimited JSON-RPC over its stdin/stdout.
type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   *os.File
	writeMu sync.Mutex
	exited  chan struct{}

	closeOnce sync.Once
}

func startStdio(cfg ServerConfig, deliver func([]byte), fail func(error), logLine func(string)) (*stdioTransport, error) {
	command := strings.TrimSpace(cfg.Command)
	path, err := tools.LookCommand(command)
	if err != nil {
		return nil, fmt.Errorf("command %q not found on PATH", command)
	}
	cmd := exec.Command(path, cfg.Args...)
	cmd.Dir = cfg.Cwd
	cmd.Env = tools.ChildEnv(envList(cfg.Env)...)
	setProcessGroup(cmd)

	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW)
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW, outR, outW)
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	if err := cmd.Start(); err != nil {
		closeAll(inR, inW, outR, outW, errR, errW)
		return nil, fmt.Errorf("start %q: %w", command, err)
	}
	// The child holds its own copies now.
	closeAll(inR, outW, errW)

	t := &stdioTransport{cmd: cmd, stdin: inW, exited: make(chan struct{})}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(errR)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			logLine(sc.Text())
		}
		_ = errR.Close()
	}()
	go func() {
		r := bufio.NewReaderSize(outR, 64<<10)
		for {
			line, err := readLine(r)
			if len(line) > 0 {
				deliver(line)
			}
			if err != nil {
				break
			}
		}
		_ = outR.Close()
	}()
	go func() {
		werr := cmd.Wait()
		close(t.exited)
		// Let the last stderr lines land so the failure explains itself.
		select {
		case <-stderrDone:
		case <-time.After(300 * time.Millisecond):
		}
		// Grandchildren (npx -> node) may outlive the leader.
		killGroup(cmd)
		_ = t.stdin.Close()
		if werr == nil {
			werr = errors.New("exited")
		}
		fail(fmt.Errorf("server process %s", describeExit(werr)))
	}()
	return t, nil
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxStdioMessage {
			// Skip the rest of an oversized line.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
			return nil, err
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" && err == nil {
			line = line[:0]
			continue
		}
		return []byte(trimmed), err
	}
}

func describeExit(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "exited: " + ee.Error()
	}
	return err.Error()
}

func (t *stdioTransport) send(ctx context.Context, msg []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	select {
	case <-t.exited:
		return errors.New("server process has exited")
	default:
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = t.stdin.SetWriteDeadline(dl)
	} else {
		_ = t.stdin.SetWriteDeadline(time.Time{})
	}
	_, err := t.stdin.Write(append(msg, '\n'))
	return err
}

// close asks politely (stdin EOF), then SIGTERM, then SIGKILL.
func (t *stdioTransport) close() error {
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()
		if waitFor(t.exited, time.Second) {
			return
		}
		terminateGroup(t.cmd)
		if waitFor(t.exited, 2*time.Second) {
			return
		}
		killGroup(t.cmd)
		waitFor(t.exited, 2*time.Second)
	})
	return nil
}

func (t *stdioTransport) pid() int {
	if t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

func (t *stdioTransport) setProtocolVersion(string) {}

func waitFor(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func envList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func closeAll(cs ...io.Closer) {
	for _, c := range cs {
		_ = c.Close()
	}
}
