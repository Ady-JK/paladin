// server.go is one running language server for one project root: its
// process, the documents it has open, and the latest diagnostics it
// published per file.

package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Diagnostic severities from the protocol.
const (
	SeverityError   = 1
	SeverityWarning = 2
)

// settle is how long to keep listening after the first publish for a
// file, since some servers publish twice (syntax, then types).
const settle = 200 * time.Millisecond

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspDiagnostic struct {
	Range struct {
		Start lspPosition `json:"start"`
		End   lspPosition `json:"end"`
	} `json:"range"`
	Severity int             `json:"severity"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

type published struct {
	version int
	at      time.Time
	diags   []lspDiagnostic
}

// server is one language server process.
type server struct {
	spec Spec
	root string
	cmd  *exec.Cmd
	c    *conn
	// ready closes when initialize finished (initErr says how).
	ready   chan struct{}
	initErr error
	errOut  *capped

	// syncMu orders document updates: reading a file, numbering its
	// version and sending it happen as one step, so a server never sees
	// version n+1 before n, or older content under a newer version.
	syncMu sync.Mutex

	mu       sync.Mutex
	versions map[string]int // open documents by URI
	diags    map[string]published
	changed  chan struct{} // closed and replaced on every publish
	lastUse  time.Time
	exited   chan struct{}
}

func startServer(spec Spec, root, bin string) (*server, error) {
	argv := append([]string{bin}, spec.Command[1:]...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = tools.ChildEnv(spec.Env...)
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &server{spec: spec, root: root, cmd: cmd, ready: make(chan struct{}), versions: map[string]int{},
		diags: map[string]published{}, changed: make(chan struct{}), lastUse: time.Now(), exited: make(chan struct{})}
	s.errOut = &capped{max: 16 * 1024}
	cmd.Stderr = s.errOut
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s.c = newConn(stdout, stdin, s.onNotify, s.onRequest)
	go func() {
		_ = cmd.Wait()
		s.c.close()
		close(s.exited)
	}()
	go s.initialize()
	return s, nil
}

func (s *server) initialize() {
	defer close(s.ready)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rootURI := fileURI(s.root)
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"rootPath":  s.root,
		"workspaceFolders": []map[string]string{
			{"uri": rootURI, "name": filepath.Base(s.root)},
		},
		"clientInfo": map[string]string{"name": "paladin"},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": true, "dynamicRegistration": false},
				"publishDiagnostics": map[string]any{"versionSupport": true, "relatedInformation": false},
			},
			"workspace": map[string]any{"configuration": true, "workspaceFolders": true},
			"window":    map[string]any{"workDoneProgress": false},
		},
	}
	if err := s.c.call(ctx, "initialize", params, nil); err != nil {
		s.initErr = fmt.Errorf("initialize: %w", err)
		return
	}
	if err := s.c.notify("initialized", map[string]any{}); err != nil {
		s.initErr = err
	}
}

// onRequest answers what servers ask of a client: configuration
// (one null per item, meaning "defaults"), progress and registrations.
func (s *server) onRequest(method string, params json.RawMessage) any {
	if method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		return make([]any, len(p.Items))
	}
	return nil
}

func (s *server) onNotify(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string          `json:"uri"`
		Version     *int            `json:"version"`
		Diagnostics []lspDiagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pub := published{at: time.Now(), diags: p.Diagnostics, version: -1}
	if p.Version != nil {
		pub.version = *p.Version
	}
	s.diags[normURI(p.URI)] = pub
	close(s.changed)
	s.changed = make(chan struct{})
}

// alive reports whether the process is still running.
func (s *server) alive() bool {
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

// waitReady waits for initialize; false on failure or timeout.
func (s *server) waitReady(ctx context.Context) error {
	select {
	case <-s.ready:
		return s.initErr
	case <-s.exited:
		return fmt.Errorf("%s exited: %s", s.spec.ID, strings.TrimSpace(s.stderrTail()))
	case <-ctx.Done():
		return ctx.Err()
	}
}

// open tells the server about path without waiting (warm-up). A
// document already open is left alone: a later edit syncs it anyway.
func (s *server) open(path string) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.Lock()
	_, opened := s.versions[fileURI(path)]
	s.mu.Unlock()
	if !opened {
		s.syncLocked(path)
	}
}

// sync sends path's current text: didOpen the first time, then a full
// didChange plus didSave. It returns the document version and when it
// was sent; ok is false when the file cannot be read or is too large.
func (s *server) sync(path string) (int, time.Time, bool) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncLocked(path)
}

func (s *server) syncLocked(path string) (int, time.Time, bool) {
	text, ok := readDoc(path)
	if !ok {
		return 0, time.Time{}, false
	}
	uri := fileURI(path)
	s.mu.Lock()
	v, opened := s.versions[uri]
	v++
	s.versions[uri] = v
	s.lastUse = time.Now()
	s.mu.Unlock()
	sent := time.Now()
	if !opened {
		_ = s.c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": languageID(s.spec, path), "version": v, "text": text,
		}})
	} else {
		_ = s.c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": v},
			"contentChanges": []map[string]any{{"text": text}},
		})
	}
	_ = s.c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}, "text": text})
	return v, sent, true
}

// readDoc reads path when it is at most maxDocBytes.
func readDoc(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDocBytes+1))
	if err != nil || len(data) > maxDocBytes {
		return "", false
	}
	return string(data), true
}

// fitsDoc reports whether path is small enough to send.
func fitsDoc(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular() && st.Size() <= maxDocBytes
}

// diagnose syncs path and waits up to timeout for diagnostics newer
// than the sync. ok is false when none arrived in time.
func (s *server) diagnose(ctx context.Context, path string, timeout time.Duration) ([]lspDiagnostic, bool) {
	version, sent, ok := s.sync(path)
	if !ok {
		return nil, false
	}
	uri := fileURI(path)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var got *published
	var settleC <-chan time.Time
	for {
		s.mu.Lock()
		pub, have := s.diags[uri]
		wait := s.changed
		s.mu.Unlock()
		if have && (pub.version >= version || (pub.version < 0 && !pub.at.Before(sent))) {
			p := pub
			got = &p
			if settleC == nil {
				settleC = time.After(settle)
			}
		}
		select {
		case <-wait:
		case <-settleC:
			return got.diags, true
		case <-deadline.C:
			if got != nil {
				return got.diags, true
			}
			return nil, false
		case <-ctx.Done():
			return nil, false
		case <-s.exited:
			return nil, false
		}
	}
}

// stop asks the server to shut down and kills it if it lingers.
func (s *server) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s.alive() {
		_ = s.c.call(ctx, "shutdown", nil, nil)
		_ = s.c.notify("exit", nil)
	}
	select {
	case <-s.exited:
	case <-time.After(2 * time.Second):
		killProcessGroup(s.cmd)
		<-s.exited
	}
}

func (s *server) stderrTail() string {
	s.errOut.mu.Lock()
	defer s.errOut.mu.Unlock()
	out := s.errOut.buf.String()
	if len(out) > 500 {
		out = out[len(out)-500:]
	}
	return out
}

func (s *server) pid() int {
	if s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// fileURI is the file:// URI of an absolute path.
func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// normURI makes server URIs comparable with ours (servers may escape
// differently).
func normURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return fileURI(filepath.FromSlash(u.Path))
}

// capped keeps the first max bytes written to it.
type capped struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
