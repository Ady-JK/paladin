package memory

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Tool is the model-facing `memory` tool.
type Tool struct{ bank *Bank }

func NewTool(bank *Bank) *Tool { return &Tool{bank: bank} }

func (*Tool) Name() string { return "memory" }

func (*Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "memory",
		Description: "Save durable facts to persistent memory that survives across sessions. " +
			"Memory is injected into the system prompt of every future session, so keep entries compact and high-signal. " +
			"Targets: `user` — who the user is (role, preferences, communication style); " +
			"`global` — your notes that apply everywhere (machine setup, tools, lessons learned); " +
			"`project` — notes for the current workspace folder (build/test commands, conventions, architecture, gotchas), shared with every session in this folder. " +
			"Batch every change in one call: operations run all-or-nothing against the latest saved state. " +
			"Use `patch` to update an entry instead of adding near-duplicates, and `remove` stale ones; when a store is full, consolidate in the same batch. " +
			"`old_text` is matched exactly first, then ignoring quote and dash style and line wrapping, so retyping stored text works; " +
			"a whole-entry match wins over a substring, and an ambiguous match lists the candidates with their line numbers. " +
			"Secret-shaped values are replaced with [REDACTED] on the way in, and injection patterns are refused with the rule that matched. " +
			"Do NOT store task progress, temporary state, secrets, or raw data/logs. Omit operations to view a store's current entries (includes writes from other sessions). " +
			"Writes are saved immediately; the memory shown in your system prompt is the snapshot taken when this session started.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type":        "string",
					"enum":        []string{"user", "global", "project"},
					"description": "Which store to read or change.",
				},
				"operations": map[string]any{
					"type":        "array",
					"description": "Changes applied atomically in order. Omit to view the store.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"action":   map[string]any{"type": "string", "enum": []string{"add", "patch", "remove"}},
							"content":  map[string]any{"type": "string", "description": "New entry text (add) or replacement for old_text (patch)."},
							"old_text": map[string]any{"type": "string", "description": "Text identifying the entry to patch or remove; matched exactly first, then ignoring quote/dash style and wrapping."},
						},
						"required": []string{"action"},
					},
				},
			},
			"required": []string{"target"},
		},
	}
}

func (t *Tool) Call(raw json.RawMessage, ctx *tools.ToolCtx) (string, error) {
	var args struct {
		Target     string `json:"target"`
		Operations []Op   `json:"operations"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", tools.ArgsError(fmt.Sprintf("memory: the arguments are not valid JSON: %v", err))
	}
	target, err := ParseTarget(args.Target)
	if err != nil {
		return "", tools.ArgsError("memory: " + err.Error())
	}
	cwd := ""
	if ctx != nil {
		cwd = ctx.Workspace
		if ctx.Project != "" {
			cwd = ctx.Project
		}
	}
	var res Result
	if len(args.Operations) == 0 {
		res, err = t.bank.Read(target, cwd)
	} else {
		res, err = t.apply(target, cwd, args.Operations)
		if err == nil && ctx != nil && ctx.SessionDir != "" {
			// The turn counter lives beside the transcript: a write resets
			// the review nudge, a turn without one advances it.
			_ = NoteWrite(ctx.SessionDir)
		}
	}
	if err != nil {
		msg := "memory: " + err.Error()
		if strings.Contains(err.Error(), "limit exceeded") || strings.Contains(err.Error(), "matches more than one") || strings.Contains(err.Error(), "no entry contains") {
			if cur, rerr := t.bank.Read(target, cwd); rerr == nil {
				data, _ := jsonx.Marshal(map[string]any{"error": msg, "entries": cur.Entries, "used": cur.Used, "limit": cur.Limit})
				return "", tools.WithContent(string(data), msg)
			}
		}
		return "", tools.ArgsError(msg)
	}
	out := map[string]any{
		"target":  string(target),
		"entries": res.Entries,
		"used":    res.Used,
		"limit":   res.Limit,
	}
	if len(args.Operations) > 0 {
		out["saved"] = true
	}
	if len(res.Masked) > 0 {
		out["masked"] = res.Masked
		out["note"] = "secret-shaped values in the listed entries were replaced with " + Masked + " before saving; the entry still records the fact, never the credential"
	}
	data, err := jsonx.Marshal(out)
	if err != nil {
		return "", tools.ArgsError("memory: encode result: " + err.Error())
	}
	return string(data), nil
}

// apply runs a batch, turning a concurrent external edit into an
// actionable message instead of a silent overwrite: the store re-reads
// disk under the lock, so a patch whose old_text another writer already
// changed fails with the current entries attached.
func (t *Tool) apply(target Target, cwd string, ops []Op) (Result, error) {
	res, err := t.bank.Apply(target, cwd, ops)
	if err == nil {
		return res, nil
	}
	if d := driftHint(err); d != "" {
		return Result{}, fmt.Errorf("%w\n%s", err, d)
	}
	return Result{}, err
}

// driftHint explains what to do when old_text is gone, which is nearly
// always because another session rewrote the entry since this one read
// its snapshot.
func driftHint(err error) string {
	msg := err.Error()
	if !strings.Contains(msg, "no entry contains") {
		return ""
	}
	return "hint: another session may have rewritten this entry since your snapshot was taken; re-read the store (omit operations) and retry with the current text"
}
