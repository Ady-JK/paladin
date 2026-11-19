// copilot_headers.go builds the per-request identity Copilot's API expects
// on every call, matching the VS Code Copilot extension: a request id, the
// interaction (one user prompt and the rounds it triggers), what kind of
// call it is, and whether the user or the agent loop initiated it.
//
// The initiator decides premium-request counting server-side, so it follows
// VS Code's rule: only the first round of a fresh prompt is the user's.

package inference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Ady-JK/paladin/core/internal/atomicfile"
	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/google/uuid"
)

// CallKind says what a model call is for.
type CallKind string

const (
	CallAgent      CallKind = "agent"
	CallSubagent   CallKind = "subagent"
	CallCompaction CallKind = "compaction"
)

// CallMeta describes one model call to the provider.
type CallMeta struct {
	// InteractionID groups every call a single user prompt causes.
	InteractionID string
	Kind          CallKind
	// UserInitiated is true only for the first round of a fresh user prompt;
	// nil lets the provider judge from the last message.
	UserInitiated *bool
}

type callMetaKey struct{}

// WithCallMeta attaches call metadata to ctx for the provider call.
func WithCallMeta(ctx context.Context, m CallMeta) context.Context {
	return context.WithValue(ctx, callMetaKey{}, m)
}

func CallMetaFrom(ctx context.Context) CallMeta {
	m, _ := ctx.Value(callMetaKey{}).(CallMeta)
	return m
}

// copilotAPIVersion is sent on the model listing and on every request. VS Code
// pins an older version, but Copilot only returns the tiered listing (the 1M
// window, its 936k input limit and the long-context prices) from 2026-06-01 on;
// older versions get the flat 200k listing. Requests use the version the
// listing was read with so the limits they rely on are the ones advertised.
const copilotAPIVersion = "2026-08-01"

var (
	sessionIDOnce sync.Once
	sessionID     string
	machineIDMu   sync.Mutex
	machineIDs    = map[string]string{}
)

// processSessionID is stable for the life of the sidecar, like VS Code's
// per-window session id.
func processSessionID() string {
	sessionIDOnce.Do(func() { sessionID = uuid.NewString() })
	return sessionID
}

// installMachineID is a random id made once per install and kept in the data
// directory. It is not derived from the hardware or the user.
func installMachineID() string {
	dir := config.DataDir()
	machineIDMu.Lock()
	defer machineIDMu.Unlock()
	if id, ok := machineIDs[dir]; ok {
		return id
	}
	path := filepath.Join(dir, "machine-id")
	if raw, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			machineIDs[dir] = id
			return id
		}
	}
	id := uuid.NewString()
	if err := os.MkdirAll(dir, 0o700); err == nil {
		// A failed write only means the id is not remembered next launch.
		_ = atomicfile.WriteMode(path, []byte(id+"\n"), 0o600)
	}
	machineIDs[dir] = id
	return id
}

// copilotRequestHeaders is the identity block for one Copilot call; nil for
// any other provider.
func (p *OpenAIProvider) copilotRequestHeaders(ctx context.Context, messages []LlmMessage) map[string]string {
	if !p.isCopilot {
		return nil
	}
	meta := CallMetaFrom(ctx)
	requestID := uuid.NewString()
	interaction := meta.InteractionID
	if interaction == "" {
		interaction = uuid.NewString()
	}
	interactionType := "conversation-agent"
	switch meta.Kind {
	case CallSubagent:
		interactionType = "conversation-subagent"
	case CallCompaction:
		interactionType = "conversation-compaction"
	}
	initiator := "agent"
	if meta.UserInitiated != nil {
		if *meta.UserInitiated {
			initiator = "user"
		}
	} else if lastRole(messages) == "user" {
		initiator = "user"
	}
	h := map[string]string{
		"X-Request-Id":         requestID,
		"X-Agent-Task-Id":      requestID,
		"X-Interaction-Id":     interaction,
		"X-Interaction-Type":   interactionType,
		"Openai-Intent":        "conversation-agent",
		"X-Initiator":          initiator,
		"X-GitHub-Api-Version": copilotAPIVersion,
		"VScode-SessionId":     processSessionID(),
		"VScode-MachineId":     installMachineID(),
	}
	for _, m := range messages {
		if m.Image != nil {
			h["Copilot-Vision-Request"] = "true"
			break
		}
	}
	return h
}

// lastRole is the role of the final message that is not a system prompt.
func lastRole(messages []LlmMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "system" {
			return messages[i].Role
		}
	}
	return ""
}
