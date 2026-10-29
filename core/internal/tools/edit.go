// Package tools: edit.go replaces text verbatim in an existing file.
package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

type EditTool struct {
	engine Engine
}

func NewEditTool(engine Engine) *EditTool { return &EditTool{engine: engine} }

func (EditTool) Name() string { return "edit" }

func (EditTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "edit",
		Description: "Replace text verbatim in an existing file. Pass the path as a full " +
			"absolute path; bare relative paths are rejected. " +
			"old_string should match " +
			"exactly as it appears in the file, so read the file first; small drift " +
			"(line endings, trailing whitespace, typographic quotes/dashes, " +
			"indentation level) is tolerated and reported as match_strategy. When " +
			"old_string occurs more than once the call fails and lists every " +
			"occurrence with its line numbers and the nearest non-blank lines " +
			"above and below: retry with more surrounding lines in old_string so " +
			"it matches exactly once, or pass replace_all true to replace every " +
			"occurrence. Edits outside the workspace are allowed but reported as outside.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "The file to edit as a full absolute path."},
				"old_string":  map[string]any{"type": "string", "description": "The exact text to find in the file. Copy it verbatim from the file."},
				"new_string":  map[string]any{"type": "string", "description": "The replacement text. May be empty to delete old_string."},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence of old_string. Defaults to false, which requires exactly one match."},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

func (e *EditTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Path       string `json:"path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("edit: the arguments are not valid JSON: %v", err))
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", ArgsError("edit: path is required")
	}
	if args.OldString == "" {
		return "", ArgsError("edit: old_string is required and cannot be empty")
	}
	res, err := e.engine.Edit(ctx.Workspace, args.Path, args.OldString, args.NewString, args.ReplaceAll)
	if err != nil {
		if eeng, ok := err.(*EngineError); ok {
			switch eeng.Kind {
			case "notfound":
				content, _ := jsonx.Marshal(map[string]any{
					"error":          "edit: old text not found in file; read the file and copy the exact text",
					"requested_path": eeng.RequestedPath,
					"resolved_path":  eeng.ResolvedPath,
					"scope":          eeng.Scope,
				})
				return "", WithContent(string(content), "edit: old text not found in file; read the file and copy the exact text")
			case "ambiguous":
				type wireLine struct {
					Line int    `json:"line"`
					Text string `json:"text"`
				}
				type occurrencePayload struct {
					Occurrence int       `json:"occurrence"`
					StartLine  int       `json:"start_line"`
					EndLine    int       `json:"end_line"`
					Before     *wireLine `json:"before"`
					After      *wireLine `json:"after"`
				}
				convert := func(l *ContextLine) *wireLine {
					if l == nil {
						return nil
					}
					return &wireLine{Line: l.Line, Text: l.Text}
				}
				occ := make([]occurrencePayload, 0, len(eeng.Occurrences))
				for _, o := range eeng.Occurrences {
					occ = append(occ, occurrencePayload{
						Occurrence: o.Occurrence,
						StartLine:  o.StartLine,
						EndLine:    o.EndLine,
						Before:     convert(o.Before),
						After:      convert(o.After),
					})
				}
				msg := fmt.Sprintf("edit: old text occurs %d times; retry with more context around the one you want, or replace all", eeng.Count)
				content, _ := jsonx.Marshal(map[string]any{
					"error":          msg,
					"requested_path": eeng.RequestedPath,
					"resolved_path":  eeng.ResolvedPath,
					"scope":          eeng.Scope,
					"count":          eeng.Count,
					"occurrences":    occ,
					"hint":           "copy more surrounding lines into old_string so it matches exactly once, or pass replace_all true",
				})
				return "", WithContent(string(content), msg)
			}
		}
		return "", ArgsError(err.Error())
	}
	out := map[string]any{
		"requested_path":   res.RequestedPath,
		"resolved_path":    res.ResolvedPath,
		"scope":            res.Scope,
		"replacements":     res.Replacements,
		"bytes":            res.Bytes,
		"lines":            res.Lines,
		"match_start_line": res.MatchStartLine,
		"match_end_line":   res.MatchEndLine,
		"matches":          res.Matches,
		"match_strategy":   res.Strategy,
	}
	if res.Strategy != "" && res.Strategy != MatchExact {
		out["note"] = "old_string did not match verbatim; matched after normalizing " + strings.ReplaceAll(res.Strategy, "_", " ") + ". Re-read the file before further edits to copy its exact text."
	}
	// Matches serialize as [{start_line,end_line}]; an empty list must
	// still be present (serde emits []).
	data, merr := jsonx.Marshal(out)
	if merr != nil {
		return "", ArgsError(fmt.Sprintf("edit: encode the result: %v", merr))
	}
	return string(data), nil
}
