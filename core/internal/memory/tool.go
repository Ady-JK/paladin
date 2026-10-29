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
							"old_text": map[string]any{"type": "string", "description": "Unique text identifying the entry to patch or remove."},
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
		res, err = t.bank.Apply(target, cwd, args.Operations)
	}
	if err != nil {
		msg := "memory: " + err.Error()
		if strings.Contains(err.Error(), "limit exceeded") {
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
	data, err := jsonx.Marshal(out)
	if err != nil {
		return "", tools.ArgsError("memory: encode result: " + err.Error())
	}
	return string(data), nil
}
