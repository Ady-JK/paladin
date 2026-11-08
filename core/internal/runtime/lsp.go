// lsp.go connects a turn's write, edit and read tools to the language
// server manager, and reports server status for Settings → Tools.

package runtime

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/Ady-JK/paladin/core/internal/tools"
)

// lspHooks are a turn's Diagnose and Warm. The workspace is the
// session's (a worktree session diagnoses its own checkout).
func (r *AgentRuntime) lspHooks(ctx context.Context, cwd, sessionID string) (func(string, func(string) bool) tools.Diagnosis, func(string, func(string) bool)) {
	workspace := cwd
	if sess, err := r.transcripts.Load(cwd, sessionID); err == nil && sess.Meta.Cwd != "" {
		workspace = sess.Meta.Cwd
	}
	diagnose := func(path string, trust func(string) bool) tools.Diagnosis {
		res := r.lsp.Diagnose(ctx, workspace, path, trust)
		return tools.Diagnosis{Server: res.Server, Diagnostics: res.Diagnostics, Errors: res.Errors}
	}
	warm := func(path string, trust func(string) bool) { r.lsp.Warm(workspace, path, trust) }
	return diagnose, warm
}

type IDEDiagnostics struct {
	State       string             `json:"state"`
	Server      string             `json:"server,omitempty"`
	Diagnostics []tools.Diagnostic `json:"diagnostics"`
	Errors      int                `json:"errors"`
}

// IDEDiagnose uses a separate LSP manager so an editor's unsaved buffer cannot
// alter the server state used by agent tools. Project-shipped code is not run.
func (r *AgentRuntime) IDEDiagnose(cwd, path, content string) (IDEDiagnostics, error) {
	out := IDEDiagnostics{Diagnostics: []tools.Diagnostic{}}
	resolved, err := ideFilePath(cwd, path)
	if err != nil {
		return out, err
	}
	if !utf8.ValidString(content) || len(content) > 1<<20 {
		return out, fmt.Errorf("editor diagnostics require UTF-8 text up to 1 MiB")
	}
	if !r.loadSettings().lspDiagnostics() {
		out.State = "disabled"
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result := r.editorLsp.DiagnoseText(ctx, cwd, resolved, content, func(string) bool { return false })
	out.Server, out.Diagnostics, out.Errors = result.Server, result.Diagnostics, result.Errors
	if out.Diagnostics == nil {
		out.Diagnostics = []tools.Diagnostic{}
	}
	switch {
	case result.Server == "" || result.Unavailable:
		out.State = "unavailable"
	case result.Timedout:
		out.State = "timeout"
	default:
		out.State = "ready"
	}
	return out, nil
}
