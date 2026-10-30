package hooks

import "encoding/json"

// Input is the JSON a hook reads on stdin. The field names are the
// Claude/Codex wire contract, so they stay snake_case.
type Input struct {
	Event          Event  `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd"`
	Model          string `json:"model,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`

	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`

	Prompt               string `json:"prompt,omitempty"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	StopHookActive       bool   `json:"stop_hook_active,omitempty"`
	Source               string `json:"source,omitempty"`  // SessionStart: startup|resume|compact
	Trigger              string `json:"trigger,omitempty"` // Pre/PostCompact: manual|auto
}
