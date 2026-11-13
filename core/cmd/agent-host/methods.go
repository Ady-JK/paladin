package main

import (
	"context"
	"encoding/json"
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
	"terminalStart": func(_ *runtime.AgentRuntime, a args) (any, error) {
		cols, rows := a.num("cols", 80), a.num("rows", 24)
		if cols < 1 || cols > 1000 || rows < 1 || rows > 1000 {
			return nil, tools.ArgsError("invalid terminal size")
		}
		return userTerminals.start(a.str("sessionId"), a.str("cwd"), uint16(cols), uint16(rows))
	},
	"terminalWrite": func(_ *runtime.AgentRuntime, a args) (any, error) {
		return true, userTerminals.write(a.str("sessionId"), a.str("terminalId"), a.str("data"))
	},
	"terminalResize": func(_ *runtime.AgentRuntime, a args) (any, error) {
		cols, rows := a.num("cols", 0), a.num("rows", 0)
		if cols < 1 || cols > 1000 || rows < 1 || rows > 1000 {
			return nil, tools.ArgsError("invalid terminal size")
		}
		return true, userTerminals.resize(a.str("sessionId"), a.str("terminalId"), uint16(cols), uint16(rows))
	},
	"terminalClose": func(_ *runtime.AgentRuntime, a args) (any, error) {
		return true, userTerminals.close(a.str("sessionId"), a.str("terminalId"))
	},
	"terminalRun": func(_ *runtime.AgentRuntime, a args) (any, error) {
		return terminalRun(a.str("command"), a.str("cwd"))
	},
	"browserReply": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var reply tools.BrowserReply
		if err := a.decode(&reply); err != nil {
			return nil, err
		}
		return rt.BrowserReply(reply), nil
	},
	"ping": func(*runtime.AgentRuntime, args) (any, error) {
		return map[string]bool{"ok": true}, nil
	},

	// Providers
	"listProviders": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.ListProviders(), nil
	},
	"copilotSignInStart": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.StartCopilotSignIn()
	},
	"copilotSignInPoll": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.PollCopilotSignIn()
	},
	"copilotSignInCancel": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		rt.CancelCopilotSignIn()
		return true, nil
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
	"ideList": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.IDEList(a.str("cwd"), a.str("path"), runtime.IDEExplorerSettings{HideIgnored: a.flag("hideIgnored"), HideDotFolders: a.flag("hideDotFolders")})
	},
	"ideSearch": func(_ *runtime.AgentRuntime, a args) (any, error) {
		return tools.IDESearch(a.str("cwd"), a.str("query"))
	},
	"ideDiagnose": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var params struct {
			Cwd     string  `json:"cwd"`
			Path    string  `json:"path"`
			Content *string `json:"content"`
		}
		if err := a.decode(&params); err != nil {
			return nil, err
		}
		if params.Content == nil {
			return nil, tools.ArgsError("editor diagnostics require content")
		}
		return rt.IDEDiagnose(params.Cwd, params.Path, *params.Content)
	},
	"ideLayoutGet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.IDELayoutGet(a.str("cwd"))
	},
	"ideLayoutSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var params struct {
			Cwd    string          `json:"cwd"`
			Layout json.RawMessage `json:"layout"`
		}
		if err := a.decode(&params); err != nil {
			return nil, err
		}
		return true, rt.IDELayoutSet(params.Cwd, params.Layout)
	},
	"ideExplorerSettingsGet": func(_ *runtime.AgentRuntime, a args) (any, error) {
		return runtime.IDEExplorerSettingsGet(a.str("cwd"))
	},
	"ideExplorerSettingsSet": func(_ *runtime.AgentRuntime, a args) (any, error) {
		return true, runtime.IDEExplorerSettingsSet(a.str("cwd"), runtime.IDEExplorerSettings{HideIgnored: a.flag("hideIgnored"), HideDotFolders: a.flag("hideDotFolders")})
	},
	"ideReadFile": func(_ *runtime.AgentRuntime, a args) (any, error) {
		return runtime.IDEReadFile(a.str("cwd"), a.str("path"))
	},
	"ideFileStatuses": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var params struct {
			Cwd   string                 `json:"cwd"`
			Files []runtime.IDEFileCheck `json:"files"`
		}
		if err := a.decode(&params); err != nil {
			return nil, err
		}
		return rt.IDEFileStatuses(params.Cwd, params.Files)
	},
	"ideSaveFile": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var params struct {
			Cwd     string  `json:"cwd"`
			Path    string  `json:"path"`
			Content *string `json:"content"`
			Version string  `json:"version"`
		}
		if err := a.decode(&params); err != nil {
			return nil, err
		}
		if params.Content == nil {
			return nil, tools.ArgsError("IDE save requires text content")
		}
		return rt.CreatorSaveIDEFile(params.Cwd, params.Path, *params.Content, params.Version)
	},
	"creatorModeGet": func(rt *runtime.AgentRuntime, _ args) (any, error) { return rt.CreatorMode(), nil },
	"creatorModeSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return true, rt.SetCreatorMode(a.flag("enabled"))
	},
	"creatorWorkspace": func(rt *runtime.AgentRuntime, _ args) (any, error) { return rt.CreatorWorkspace() },
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
	"suggestPrompt": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SuggestPrompt(a.str("systemPrompt"), a.str("instruction"), a.str("provider"), a.str("model"))
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
	"memorySnapshotStatus": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.MemorySnapshotStatus(a.str("sessionId"), a.str("cwd"))
	},
	"memoryConfig": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.MemoryConfig(), nil
	},
	"contextSources": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ContextSources(a.str("cwd")), nil
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
	"sessionHistoryPage": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionHistoryPage(a.str("sessionId"), a.str("cwd"), a.num("limit", runtime.HistoryLimit), a.num("before", 0))
	},
	"sessionImages": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionImages(a.str("sessionId"), a.str("cwd"))
	},
	"saveAttachment": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SaveAttachment(a.str("sessionId"), a.str("cwd"), a.str("name"), a.str("data"))
	},
	"saveTaggedImage": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SaveTaggedImage(a.str("sessionId"), a.str("cwd"), a.str("path"))
	},
	"sessionImage": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionImage(a.str("sessionId"), a.str("cwd"), a.str("path"))
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
	"compactionGet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.CompactionGetForSession(a.str("provider"), a.str("model"), a.str("sessionId"), a.str("cwd"))
	},
	"compactionSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			Provider  string  `json:"provider"`
			Model     string  `json:"model"`
			Percent   float64 `json:"percent"`
			Window    *int64  `json:"window"`
			SessionID string  `json:"sessionId"`
			Cwd       string  `json:"cwd"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.CompactionSetForSession(p.Provider, p.Model, p.Percent, p.Window, p.SessionID, p.Cwd)
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
	"sessionChanges": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionChanges(a.str("sessionId"), a.str("cwd"))
	},
	"sessionDiff": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.SessionDiff(a.str("sessionId"), a.str("cwd"), a.num("turn", -1), a.str("path"))
	},
	"fileExcerpt": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.FileExcerpt(a.str("sessionId"), a.str("cwd"), a.str("path"), a.num("start", 1), a.num("end", 0))
	},
	"filePreview": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.FilePreview(a.str("cwd"), a.str("path"))
	},
	"revertSession": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.RevertSessionChecked(a.str("sessionId"), a.str("cwd"), a.num("index", -1), a.str("version"))
	},
	"recoveryAvailable": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.RecoveryAvailable(a.str("sessionId"), a.str("cwd"))
	},
	"recoveryPreview": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.RecoveryPreview(a.str("sessionId"), a.str("cwd"))
	},
	"recoverLatest": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.RecoverLatestChecked(a.str("sessionId"), a.str("cwd"), a.str("version"))
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
		return rt.Attach(a.str("sessionId"), a.str("cwd"), a.num("limit", 0))
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
	"runningJobs": func(rt *runtime.AgentRuntime, _ args) (any, error) {
		return rt.RunningJobs(), nil
	},
	"killJob": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.KillJob(a.str("id"))
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

	// Subagents
	"listAgents": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ListAgents(a.str("sessionId"))
	},
	"interruptAgent": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.InterruptAgent(a.str("sessionId"), a.str("agentId"))
	},

	// Side questions (/btw); they run on the long pool, see methodClass.
	"sideQuestion": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var req runtime.SideQuestionRequest
		if err := a.decode(&req); err != nil {
			return nil, err
		}
		return rt.SideQuestion(context.Background(), req)
	},
	"listSideChats": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ListSideChats(a.str("sessionId"), a.str("cwd"))
	},
	"loadSideChat": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.LoadSideChat(a.str("sessionId"), a.str("cwd"), a.str("chatId"))
	},
	"deleteSideChat": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return nil, rt.DeleteSideChat(a.str("sessionId"), a.str("cwd"), a.str("chatId"))
	},

	// Hooks
	"hooksList": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.HooksList(a.str("cwd")), nil
	},
	"hooksSetConfig": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.HooksSetConfig(a.str("scope"), a.str("cwd"), a.str("text"))
	},
	"hooksTrust": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.HooksTrust(a.str("cwd"), a.str("path"), a.flag("trust"), a.str("fingerprint"))
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
		return rt.MCPTrust(a.str("cwd"), a.flag("trust"), a.str("fingerprint"))
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
	"customList": func(rt *runtime.AgentRuntime, _ args) (any, error) { return rt.CustomList(), nil },
	"customGet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.CustomGet(a.str("id"))
	},
	"customSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.CustomSet(a.str("id"), a.str("text"))
	},
	"customReset": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.CustomReset(a.str("id"))
	},
	"skillEnabledSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return true, rt.SkillEnabledSet(a.str("name"), a.flag("enabled"))
	},
	"toolBehaviorSet": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ToolBehaviorSet(a.str("key"), a.flag("enabled"), a.str("cwd"), a.str("sessionId"))
	},
	"toolsReload": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.ToolsReload(a.str("cwd"), a.str("sessionId"))
	},
	"pluginViews": func(rt *runtime.AgentRuntime, a args) (any, error) {
		return rt.PluginViews(), nil
	},
	"pluginInvoke": func(rt *runtime.AgentRuntime, a args) (any, error) {
		var p struct {
			SessionID   string          `json:"sessionId"`
			Cwd         string          `json:"cwd"`
			Plugin      string          `json:"plugin"`
			View        string          `json:"view"`
			Tool        string          `json:"tool"`
			Args        json.RawMessage `json:"args"`
			Destructive bool            `json:"destructive"`
		}
		if err := a.decode(&p); err != nil {
			return nil, err
		}
		return rt.PluginInvoke(p.SessionID, p.Cwd, p.Plugin, p.View, p.Tool, p.Args, p.Destructive)
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
