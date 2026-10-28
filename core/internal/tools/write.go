// Package tools: write.go creates a file with full content.
package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/jsonx"
)

type WriteTool struct {
	engine Engine
}

func NewWriteTool(engine Engine) *WriteTool { return &WriteTool{engine: engine} }

func (WriteTool) Name() string { return "write" }

func (WriteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "write",
		Description: "Create a file with the given content. Pass the path as a full " +
			"absolute path; bare relative paths are rejected. " +
			"Missing parent directories are created. " +
			"When the file already exists the call fails unless overwrite is true, " +
			"in which case the file is replaced wholesale. Writes outside the " +
			"workspace are allowed but reported as outside. Read the file first " +
			"when replacing one, and prefer edit for small changes to an existing file.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "description": "The file to create as a full absolute path."},
				"content":   map[string]any{"type": "string", "description": "The full content to write into the file."},
				"overwrite": map[string]any{"type": "boolean", "description": "Replace the file when it already exists. Defaults to false."},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (w *WriteTool) Call(raw json.RawMessage, ctx *ToolCtx) (string, error) {
	var args struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", ArgsError(fmt.Sprintf("write: the arguments are not valid JSON: %v", err))
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", ArgsError("write: path is required")
	}
	res, err := w.engine.Write(ctx.Workspace, args.Path, args.Content, args.Overwrite)
	if err != nil {
		if eeng, ok := err.(*EngineError); ok && eeng.Kind == "exists" {
			msg := fmt.Sprintf("write: path %q already exists; pass overwrite to replace it", eeng.RequestedPath)
			content, _ := jsonx.Marshal(map[string]any{
				"error":          msg,
				"requested_path": eeng.RequestedPath,
				"resolved_path":  eeng.ResolvedPath,
				"scope":          eeng.Scope,
				"exists":         true,
			})
			return "", WithContent(string(content), msg)
		}
		return "", ArgsError(err.Error())
	}
	data, merr := jsonx.Marshal(res)
	if merr != nil {
		return "", ArgsError(fmt.Sprintf("write: encode the result: %v", merr))
	}
	return string(data), nil
}
