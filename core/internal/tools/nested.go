// nested.go attaches subfolder instructions. The system prompt holds
// the AGENTS.md files from the project root down to the workspace; the
// first time a read, write or edit touches a file deeper in, the
// AGENTS.md (or CLAUDE.md) files between the workspace and that file's
// folder ride along in the tool result as nested_instructions. A write
// or edit is held instead of run (HoldForInstructions), so the agent
// has the rules before it changes anything there. The system prompt,
// and so the prompt cache, never changes. Which files a session already
// got is kept in its folder, so each arrives once. A file counts as
// delivered only when its text reached the agent (attached, cut with a
// note when it is too big, or read by the agent itself). One that cannot
// be read is named once; one that no longer fits the session's budget
// holds writes there until the agent reads it.

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/custom"
	"github.com/Ady-JK/paladin/core/internal/instructions"
)

const (
	nestedRecordFile = "nested-instructions.json"
	// nestedMaxBytes is one session's budget for attached files.
	nestedMaxBytes = instructions.MaxBytes
)

// nestedTools are the calls whose path argument attaches instructions.
var nestedTools = map[string]bool{"read": true, "write": true, "edit": true}

// nestedMu serializes record updates; parallel reads of one session
// would otherwise race on the file.
var nestedMu sync.Mutex

type nestedRecord struct {
	// Files reached the agent; Bytes is what attaching them cost.
	Files []string `json:"files"`
	Bytes int      `json:"bytes"`
	// Reported could not be read and were named to the agent.
	Reported []string `json:"reported,omitempty"`
}

// nestedDelivery is what one call tells the agent about its folder.
type nestedDelivery struct {
	// text is the files' contents under the header, or "".
	text string
	// missing names files that apply but were not delivered, and why.
	missing []string
	// hold: there is something the agent has not been told, or a file
	// it must still read before changing anything there.
	hold bool
}

func (d nestedDelivery) fields() map[string]any {
	out := map[string]any{}
	if d.text != "" {
		out["nested_instructions"] = d.text
	}
	if len(d.missing) > 0 {
		out["nested_instructions_missing"] = d.missing
	}
	return out
}

// heldMessage tells the agent (and the user, on the row) why a write
// or edit did not run.
const heldMessage = "Not run yet: this folder has instructions (nested_instructions) you had not seen. " +
	"Read them, then resend the call, changed if they require it. Nothing was written."

// nestedFiles is the instruction files between the workspace and the
// folder of name's path argument, or nil.
func nestedFiles(name string, args json.RawMessage, ctx *ToolCtx) []string {
	if !nestedTools[name] || ctx == nil || ctx.Workspace == "" || ctx.SessionDir == "" {
		return nil
	}
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &a) != nil || !filepath.IsAbs(strings.TrimSpace(a.Path)) {
		return nil
	}
	path := filepath.Clean(strings.TrimSpace(a.Path))
	if scopeOf(path, ctx.Workspace) != ScopeInside {
		return nil
	}
	return instructions.Below(ctx.Workspace, filepath.Dir(path))
}

// HoldForInstructions answers a write or edit into a folder whose
// instructions this session has not seen with those instructions,
// without running it. ok is false when the call should run. The agent
// asks this before hooks and the permission gate, so nobody approves a
// call that will not run.
func (r *Registry) HoldForInstructions(call *ToolCall, ctx *ToolCtx) (ToolResult, bool) {
	name, args, ok := r.Prepare(call)
	if !ok || name == "read" || !r.Mutates(name) {
		return ToolResult{}, false
	}
	found := nestedFiles(name, args, ctx)
	if len(found) == 0 {
		return ToolResult{}, false
	}
	d := attachNested(ctx.SessionDir, found, "")
	if !d.hold {
		return ToolResult{}, false
	}
	fields := d.fields()
	fields["error"] = custom.Text("note.nested-held")
	content, _ := json.Marshal(fields)
	return ToolResult{CallID: call.ID, Name: name, IsError: true, Content: string(content)}, true
}

// withNestedInstructions adds nested_instructions to a successful
// result of name when its path leads into a subfolder with unseen
// instruction files. Anything unexpected leaves content as it was.
func withNestedInstructions(name string, args json.RawMessage, content string, ctx *ToolCtx) string {
	found := nestedFiles(name, args, ctx)
	if len(found) == 0 {
		return content
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(content), &obj) != nil || obj == nil {
		return content
	}
	// Reading an instruction file delivers it.
	read := ""
	if name == "read" {
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &a) == nil {
			read = filepath.Clean(strings.TrimSpace(a.Path))
		}
	}
	d := attachNested(ctx.SessionDir, found, read)
	if !d.hold {
		return content
	}
	for k, v := range d.fields() {
		obj[k], _ = json.Marshal(v)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return content
	}
	return string(out)
}

// attachNested renders the files the session has not been given yet,
// within its remaining budget, and records them. read is a file the
// agent just read itself ("" for none), which counts as delivered.
func attachNested(sessionDir string, files []string, read string) nestedDelivery {
	nestedMu.Lock()
	defer nestedMu.Unlock()
	recordPath := filepath.Join(sessionDir, nestedRecordFile)
	var rec nestedRecord
	if data, err := os.ReadFile(recordPath); err == nil {
		_ = json.Unmarshal(data, &rec)
	}
	var d nestedDelivery
	var b strings.Builder
	changed := false
	for _, file := range files {
		if slices.Contains(rec.Files, file) {
			continue
		}
		if file == read {
			rec.Files = append(rec.Files, file)
			changed = true
			continue
		}
		left := nestedMaxBytes - rec.Bytes
		if left <= 0 {
			d.missing = append(d.missing, file+": does not fit this session's budget for folder instructions; "+
				"read it with the read tool before you change files here")
			d.hold = true
			continue
		}
		body, _, ok := instructions.ReadCapped(file, left)
		if !ok {
			if !slices.Contains(rec.Reported, file) {
				rec.Reported = append(rec.Reported, file)
				d.missing = append(d.missing, file+": could not be read, so its instructions are unknown; tell the user if they matter")
				d.hold, changed = true, true
			}
			continue
		}
		rec.Files = append(rec.Files, file)
		rec.Reported = slices.DeleteFunc(rec.Reported, func(f string) bool { return f == file })
		changed = true
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		rec.Bytes += len(body)
		b.WriteString("\n--- " + file + " ---\n" + body + "\n")
	}
	if changed {
		if data, err := json.Marshal(rec); err == nil && os.MkdirAll(sessionDir, 0o755) == nil {
			_ = atomicfile.Write(recordPath, data)
		}
	}
	if b.Len() > 0 {
		d.text = strings.TrimSpace(custom.Text("note.nested-header")) + "\n" + strings.TrimRight(b.String(), "\n")
		d.hold = true
	}
	return d
}
