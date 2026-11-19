package memory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// Tool is the model-facing `memory` tool.
type Tool struct{ bank *Bank }

func NewTool(bank *Bank) *Tool { return &Tool{bank: bank} }

func (*Tool) Name() string { return "memory" }

// ModeTitle names one memory mode for the UI and tool results. The wire
// value stays `user`/`global`/`project`, so existing stores and settings
// files keep working; only the human-facing name changed.
func ModeTitle(t Target) string {
	switch t {
	case TargetUser:
		return "User Preferences"
	case TargetGlobal:
		return "Global Rules"
	case TargetProject:
		return "Workspace Rules"
	}
	return string(t)
}

// shapeHint is appended to an argument error. Models retry far more
// reliably when shown the exact shape than when told only what was wrong.
const shapeHint = `expected shape:
  {"target": "user" | "global" | "project",
   "operations": [{"action": "add" | "patch" | "remove", "content": "...", "old_text": "..."}]}
Put target at the top level, not inside an operation; omit operations to read a mode.`

func (*Tool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "memory",
		Description: "Save durable facts to persistent memory that survives across sessions. " +
			"For each user message, notice lasting preferences, corrections, and recurring constraints; save them without waiting for an explicit request to remember. " +
			"Three modes, chosen by the top-level `target` field: " +
			"`user` (User Preferences) — who the user is, their preferred style, language, tone and how they want to be worked with; " +
			"`global` (Global Rules) — overall rules and environment facts that hold in every workspace; " +
			"`project` (Workspace Rules) — rules for the current workspace folder, shared by every session opened in it. " +
			"Pick the narrowest mode that still applies: a preference about the user goes in `user`, a rule true everywhere goes in `global`, and anything tied to this repository or folder goes in `project`. " +
			"Do not infer a lasting preference from a one-off task instruction or save temporary details. " +
			"Memory is injected into the system prompt of every future session, so keep entries compact and high-signal. " +
			"Batch every change in one call: operations run all-or-nothing against the latest saved state. " +
			"Use `patch` to update an entry instead of adding near-duplicates, and `remove` stale ones; when a store is full, consolidate in the same batch. " +
			"`old_text` is matched exactly first, then ignoring quote and dash style and line wrapping, so retyping stored text works; " +
			"a whole-entry match wins over a substring, and an ambiguous match lists the candidates with their line numbers. " +
			"Secret-shaped values are replaced with [REDACTED] on the way in, and injection patterns are refused with the rule that matched. " +
			"Do NOT store task progress, temporary state, secrets, or raw data/logs. Omit operations to view a mode's current entries (includes writes from other sessions). " +
			"Writes are saved immediately; the memory shown in your system prompt is the snapshot taken when this session started.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{
					"type": "string",
					"enum": []string{"user", "global", "project"},
					"description": "Which memory mode to read or change, at this level: " +
						"user = User Preferences, global = Global Rules, project = Workspace Rules.",
				},
				"operations": map[string]any{
					"type":        "array",
					"description": "Changes applied atomically in order. Omit (or send an empty array) to view the mode instead.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"action": map[string]any{
								"type": "string",
								"enum": []string{"add", "patch", "remove"},
							},
							"content": map[string]any{
								"type":        "string",
								"description": "New entry text (add) or the replacement span for old_text (patch).",
							},
							"old_text": map[string]any{
								"type":        "string",
								"description": "Text identifying the entry to patch or remove; matched exactly first, then ignoring quote/dash style and wrapping.",
							},
						},
						"required":             []string{"action"},
						"additionalProperties": true,
					},
				},
			},
			"required":             []string{"target"},
			"additionalProperties": true,
		},
	}
}

// Field names models reach for when they do not follow the schema to the
// letter. Accepting them costs nothing and turns a failed call into a
// saved fact, which is the whole point of the tool.
var (
	targetKeys  = []string{"target", "scope", "store", "mode", "memory"}
	opsKeys     = []string{"operations", "ops", "changes", "edits"}
	actionKeys  = []string{"action", "op", "operation", "kind"}
	contentKeys = []string{"content", "text", "entry", "value", "fact", "note", "body"}
	oldTextKeys = []string{"old_text", "oldText", "old", "match", "find", "existing", "replaces"}
)

// request is one tool call after tolerant parsing.
type request struct {
	target Target
	ops    []Op
	// readOnly is true when the call sent no operations: that means
	// "show me the mode".
	readOnly bool
}

// parseRequest reads a call without insisting on the exact schema. The
// two shapes that used to fail outright now work: a target nested inside
// an operation, and operations sent as a JSON string rather than a list.
func parseRequest(raw json.RawMessage) (request, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return request{}, fmt.Errorf("the arguments are not a JSON object: %v", err)
	}
	rawTarget := strings.TrimSpace(firstString(top, targetKeys...))
	rawOps, hasOps := firstValue(top, opsKeys...)

	var opMaps []map[string]any
	if hasOps {
		parsed, err := asOpMaps(rawOps)
		if err != nil {
			return request{}, err
		}
		opMaps = parsed
	}

	// A target inside the operations is the reported bug: hoist it when
	// the top level left it out, and refuse silently-different ones.
	opTargets := map[string]bool{}
	for _, m := range opMaps {
		if t := strings.TrimSpace(firstString(m, targetKeys...)); t != "" {
			opTargets[strings.ToLower(t)] = true
		}
	}
	if rawTarget == "" {
		if len(opTargets) == 1 {
			for t := range opTargets {
				rawTarget = t
			}
		} else if len(opTargets) > 1 {
			names := make([]string, 0, len(opTargets))
			for t := range opTargets {
				names = append(names, t)
			}
			sort.Strings(names)
			return request{}, fmt.Errorf("operations name different targets (%s); send one target per call", strings.Join(names, ", "))
		}
	} else if len(opTargets) > 0 {
		want, err := ParseTarget(rawTarget)
		if err != nil {
			return request{}, err
		}
		for t := range opTargets {
			got, gerr := ParseTarget(t)
			if gerr == nil && got != want {
				return request{}, fmt.Errorf("target %q at the top level disagrees with target %q inside an operation; send one target per call", rawTarget, t)
			}
		}
	}
	target, err := ParseTarget(rawTarget)
	if err != nil {
		return request{}, err
	}

	req := request{target: target, readOnly: len(opMaps) == 0}
	for i, m := range opMaps {
		op, perr := parseOp(m)
		if perr != nil {
			return request{}, fmt.Errorf("operation %d: %w", i+1, perr)
		}
		req.ops = append(req.ops, op)
	}
	return req, nil
}

// parseOp reads one operation, accepting the alias names above.
func parseOp(m map[string]any) (Op, error) {
	op := Op{
		Action:  strings.ToLower(strings.TrimSpace(firstString(m, actionKeys...))),
		Content: firstString(m, contentKeys...),
		OldText: firstString(m, oldTextKeys...),
	}
	if op.Action == "" {
		return Op{}, fmt.Errorf(`no action: give "action": "add", "patch" or "remove"`)
	}
	// `remove` names its entry either way round; accept the text in
	// `content` when `old_text` was left out.
	if op.Action == "remove" && strings.TrimSpace(op.OldText) == "" && strings.TrimSpace(op.Content) != "" {
		op.OldText, op.Content = op.Content, ""
	}
	return op, nil
}

// asOpMaps accepts a list of operations, a single operation object, or a
// string holding the JSON of either.
func asOpMaps(v any) ([]map[string]any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]map[string]any, 0, len(x))
		for i, item := range x {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("operation %d is not an object; send each operation as {\"action\": ...}", i+1)
			}
			out = append(out, m)
		}
		return out, nil
	case map[string]any:
		return []map[string]any{x}, nil
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, nil
		}
		var inner any
		if err := json.Unmarshal([]byte(x), &inner); err != nil {
			return nil, fmt.Errorf("operations was sent as a string that is not JSON: %v", err)
		}
		return asOpMaps(inner)
	}
	return nil, fmt.Errorf("operations must be a list of operations, not %T", v)
}

// firstString returns the first non-blank string field among keys.
func firstString(m map[string]any, keys ...string) string {
	if v, ok := firstValue(m, keys...); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// firstValue returns the first present field among keys, matching keys
// case-insensitively and ignoring a JSON null.
func firstValue(m map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		for name, v := range m {
			if !strings.EqualFold(name, key) || v == nil {
				continue
			}
			return v, true
		}
	}
	return nil, false
}

func (t *Tool) Call(raw json.RawMessage, ctx *tools.ToolCtx) (string, error) {
	req, err := parseRequest(raw)
	if err != nil {
		return "", tools.ArgsError("memory: " + err.Error() + "\n" + shapeHint)
	}
	target := req.target
	cwd := ""
	if ctx != nil {
		cwd = ctx.Workspace
		if ctx.Project != "" {
			cwd = ctx.Project
		}
	}
	var res Result
	if req.readOnly {
		res, err = t.bank.Read(target, cwd)
	} else {
		res, err = t.apply(target, cwd, req.ops)
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
		"mode":    ModeTitle(target),
		"entries": res.Entries,
		"used":    res.Used,
		"limit":   res.Limit,
	}
	if !req.readOnly {
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
