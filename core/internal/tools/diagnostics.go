// diagnostics.go carries language-server errors into write and edit
// results. The tools package only defines the shape and the hook; the
// runtime plugs a language server manager (internal/lsp) into ToolCtx.

package tools

import (
	"fmt"

	"github.com/Ady-JK/paladin/core/internal/custom"
)

// Diagnostic is one error a language server reported for a file.
type Diagnostic struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Message string `json:"message"`
	Source  string `json:"source,omitempty"`
	Code    string `json:"code,omitempty"`
}

// Diagnosis is what checking one changed file found.
type Diagnosis struct {
	Server      string
	Diagnostics []Diagnostic
	// Errors counts all errors, Diagnostics may be capped.
	Errors int
}

// diagnosticsNote tells the model what to do with reported errors.
func diagnosticsNote(d Diagnosis) string {
	more := ""
	if d.Errors > len(d.Diagnostics) {
		more = fmt.Sprintf(" (showing %d)", len(d.Diagnostics))
	}
	return custom.TextFill("note.diagnostics", map[string]string{"server": d.Server, "count": fmt.Sprint(d.Errors), "shown": more})
}

// diagnose runs the context's language server check on resolved.
func diagnose(ctx *ToolCtx, resolved string) (Diagnosis, bool) {
	if ctx == nil || ctx.Diagnose == nil || ctx.Workspace == "" || scopeOf(resolved, ctx.Workspace) != ScopeInside {
		return Diagnosis{}, false
	}
	d := ctx.Diagnose(resolved, ctx.TrustProject)
	return d, d.Server != "" && d.Errors > 0
}
