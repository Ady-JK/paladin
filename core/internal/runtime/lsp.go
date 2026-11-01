// lsp.go connects a turn's write, edit and read tools to the language
// server manager, and reports server status for Settings → Tools.

package runtime

import (
	"context"

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
