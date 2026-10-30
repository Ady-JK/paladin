package main

import (
	"context"
	"strings"

	"github.com/Ady-JK/paladin/core/internal/config"
	"github.com/Ady-JK/paladin/core/internal/mcp"
	"github.com/Ady-JK/paladin/core/internal/permission"
	"github.com/Ady-JK/paladin/core/internal/procstat"
	"github.com/Ady-JK/paladin/core/internal/runtime"
	"github.com/Ady-JK/paladin/core/internal/tools"
)

// handler answers one request; a non-nil error becomes an error response.
type handler func(rt *runtime.AgentRuntime, a args) (any, error)

// methods is the RPC table. desktop-app/PROTOCOL.md documents each one
// (a test keeps the two in sync).
var methods = map[string]handler{
	"ping": func(*runtime.AgentRuntime, args) (any, error) {
		return map[string]bool{"ok": true}, nil
	},

	// Providers
	"listProviders": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.ListProviders(), nil
	},
	"refreshProviders": func(rt *runtime.AgentRuntime, a args) (any, error) {
		// {name} refreshes one provider synchronously; no name starts
		// the background refresh-all. Both are user-initiated only.
		if name := strings.TrimSpace(a.str("name")); name != "" {
			return rt.RefreshProvider(name)
		}
		return rt.RefreshProviders(), nil
	},
	"setDefaultProvider": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SetDefaultProvider(a.str("name"))
	},
	"addProvider": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var input config.ProviderInput
		if err := a.decode(&input); err != nil {
			return nil, err
		}
		return rt.AddProvider(input)
	},
	"updateProvider": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var input config.ProviderInput
		if err := a.decode(&input); err != nil {
			return nil, err
		}
		return rt.UpdateProvider(input)
	},

	// Workspace and files
	"getWorkspace": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.GetWorkspace(), nil
	},
	"setWorkspace": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SetWorkspace(a.str("path"))
	},
	"resolveWorkspace": func(rt *runtime.AgentRuntime, a args) (any, error) {
		if cwd := strings.TrimSpace(a.str("cwd")); cwd != "" {
			return cwd, nil
		}
		return rt.FsHome(), nil
	},
	"fsHome": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.FsHome(), nil
	},
	"fsList": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.FsList(a.str("path"))
	},
	"fileComplete": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.FileComplete(a.str("query"), a.str("cwd"), a.num("limit", 20))
	},
	"gitInfo": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.GitInfo(a.str("cwd")), nil
	},

	// System prompt
	"getPrompt": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.GetPrompt(), nil
	},
	"setPrompt": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SetPrompt(a.str("systemPrompt"))
	},
	"resetPrompt": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.ResetPrompt()
	},

	// Skills, commands and memory
	"listSkills": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ListSkills(a.str("cwd")), nil
	},
	"listCommands": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ListCommands(a.str("cwd")), nil
	},
	"memoryGet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MemoryGet(a.str("target"), a.str("cwd"))
	},
	"memorySet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			Target  string   `json:"target"`
			Cwd     string   `json:"cwd"`
			Entries []string `json:"entries"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.MemorySet(p.Target, p.Cwd, p.Entries)
	},
	"memoryProjects": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.MemoryProjects(), nil
	},
	"memoryRefreshSnapshot": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return true, rt.RefreshMemorySnapshot(a.str("sessionId"), a.str("cwd"))
	},

	// Sessions
	"openSession": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.OpenSessionWith(a.str("sessionId"), a.str("cwd"), a.openOpts())
	},
	"listSessions": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ListSessions(a.str("cwd"), a.num("limit", 100))
	},
	"sessionHistory": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionHistory(a.str("sessionId"), a.str("cwd"))
	},
	"sessionTodos": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionTodos(a.str("sessionId"), a.str("cwd"))
	},
	"sessionUsage": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionUsage(a.str("sessionId"), a.str("cwd"))
	},
	"budgetGet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.BudgetGet(a.str("cwd"), a.str("sessionId"))
	},
	"budgetSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			Scope     string   `json:"scope"`
			Cwd       string   `json:"cwd"`
			SessionID string   `json:"sessionId"`
			BudgetUsd *float64 `json:"budgetUsd"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.BudgetSet(p.Scope, p.Cwd, p.SessionID, p.BudgetUsd)
	},
	"thinkingGet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ThinkingGet(a.str("provider"), a.str("model"))
	},
	"thinkingSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ThinkingSet(a.str("provider"), a.str("model"), a.str("level"))
	},
	"overallUsage": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.OverallUsage(a.str("cwd"), a.num("limit", 50))
	},
	"renameSession": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return true, rt.RenameSession(a.str("sessionId"), a.str("cwd"), a.str("title"))
	},
	"forkSession": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ForkSessionWith(a.str("sessionId"), a.str("cwd"), a.openOpts())
	},
	"deleteSession": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.DeleteSessionWith(a.str("sessionId"), a.str("cwd"), a.flag("discardWorktree"))
	},

	// Undo and worktrees
	"revertPreview": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.RevertPreview(a.str("sessionId"), a.str("cwd"), a.num("index", -1))
	},
	"revertSession": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.RevertSession(a.str("sessionId"), a.str("cwd"), a.num("index", -1))
	},
	"unrevertSession": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.UnrevertSession(a.str("sessionId"), a.str("cwd"))
	},
	"worktreeStatus": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.WorktreeStatus(a.str("sessionId"), a.str("cwd"))
	},
	"worktreeMerge": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.WorktreeMerge(a.str("sessionId"), a.str("cwd"), a.str("message"))
	},

	// Turns
	"submitChat": func(rt *runtime.AgentRuntime, a args) (any, error) {
		p, err := a.chat()
		if err != nil {
			return nil, err
		}
		info, _, err := rt.SubmitTurn(p.request(), runtime.SubmitOpts{Display: p.Display})
		return info, err
	},
	"steerChat": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SteerTurn(a.str("sessionId"), a.str("message"), a.str("display"))
	},
	"turns": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.Turns(), nil
	},
	"attach": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.Attach(a.str("sessionId"), a.str("cwd"))
	},
	"cancelTurn": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.CancelTurn(a.str("turnId")), nil
	},
	"stopChat": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.StopIn(a.str("sessionId"), a.str("cwd")), nil
	},
	"resources": func(*runtime.AgentRuntime, args) (any, error) {
		// CPU/RSS of the core plus each session's shell process trees.
		return procstat.Default.Snapshot(), nil
	},

	// Blocking tools: ask and approvals
	"answerAsk": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			SessionID string            `json:"sessionId"`
			CallID    string            `json:"callId"`
			Answers   []tools.AskAnswer `json:"answers"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.AnswerAsk(p.SessionID, p.CallID, p.Answers)
	},
	"cancelAsk": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.CancelAsk(a.str("sessionId"), a.str("callId")), nil
	},
	"answerApproval": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.AnswerApproval(a.str("sessionId"), a.str("callId"), a.str("decision"))
	},
	"permissionsGet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.PermissionsGet(a.str("cwd"), a.str("sessionId"))
	},
	"permissionsSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			Scope string            `json:"scope"`
			Cwd   string            `json:"cwd"`
			Rules []permission.Rule `json:"rules"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.PermissionsSet(p.Scope, p.Cwd, p.Rules)
	},

	// Side questions (/btw); asynchronous, see asyncMethods.
	"sideQuestion": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var req runtime.SideQuestionRequest
		if err := a.decode(&req); err != nil {
			return nil, err
		}
		return rt.SideQuestion(context.Background(), req)
	},

	// Hooks
	"hooksList": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.HooksList(a.str("cwd")), nil
	},
	"hooksSetConfig": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.HooksSetConfig(a.str("scope"), a.str("cwd"), a.str("text"))
	},
	"hooksTrust": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.HooksTrust(a.str("cwd"), a.str("path"), a.flag("trust"))
	},
	"hooksToggle": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.HooksToggle(a.str("cwd"), a.str("key"), a.flag("enabled"))
	},

	// MCP servers
	"mcpList": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MCPList(a.str("cwd")), nil
	},
	"mcpConfig": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MCPConfig(a.str("scope"), a.str("cwd"))
	},
	"mcpSetConfig": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MCPSetConfig(a.str("scope"), a.str("cwd"), a.str("text"))
	},
	"mcpSetEnabled": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MCPSetEnabled(a.str("scope"), a.str("cwd"), a.str("name"), a.flag("enabled"))
	},
	"mcpRestart": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MCPRestart(a.str("scope"), a.str("cwd"), a.str("name"))
	},
	"mcpTrust": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MCPTrust(a.str("cwd"), a.flag("trust"))
	},
	"mcpParse": func(rt *runtime.AgentRuntime, a args) (any, error) {
		list, err := mcp.ParseSnippet(a.str("text"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"servers": list}, nil
	},
	"mcpAdd": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			Scope   string            `json:"scope"`
			Cwd     string            `json:"cwd"`
			Servers []mcp.NamedServer `json:"servers"`
			Replace bool              `json:"replace"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.MCPAdd(p.Scope, p.Cwd, p.Servers, p.Replace)
	},

	// Tool switches
	"toolsList": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ToolsList(a.str("cwd"), a.str("sessionId"))
	},
	"toolsSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			Scope     string   `json:"scope"`
			Cwd       string   `json:"cwd"`
			SessionID string   `json:"sessionId"`
			Names     []string `json:"names"`
			Enabled   *bool    `json:"enabled"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.ToolsSet(p.Scope, p.Cwd, p.SessionID, p.Names, p.Enabled)
	},
	"toolsAccept": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			Cwd       string   `json:"cwd"`
			SessionID string   `json:"sessionId"`
			Names     []string `json:"names"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.ToolsAccept(p.Cwd, p.SessionID, p.Names)
	},
}
