// api.ts wraps the native backend commands.
// Single seam between the feature modules and agent-host:
// window.paladin (Electron preload) speaks JSONL to the sidecar.

import type { BudgetView, HookScope, HooksOverview, SideAnswer, GitInfo, MergeResult, OpenedSession, RevertPreview, RevertResult, WorktreeStatus, ApprovalDecision, AttachSnapshot, MCPConfigFile, MCPNamedServer, MCPOverview, MCPScope, ToolsOverview, PermissionPreset, PermissionRule, PermissionsView, CommandEntry, MemoryProject, MemoryTarget, MemoryView, TurnEvent, TurnInfo, FileEntry, FsList, MessageInfo, OverallUsage, ProviderInfo, ResourceSnapshot, RefreshResult, SessionInfo, SessionUsage, SkillEntry, TodoEntry } from "./types";
import { bridge } from "./bridge";

function invoke<T>(method: string, args: unknown = {}): Promise<T> {
  return bridge().invoke(method, args) as Promise<T>;
}

export function getWorkspace(): Promise<string> {
  return invoke<string>("getWorkspace");
}

export function resolveWorkspace(cwd: string | null): Promise<string> {
  return invoke<string>("resolveWorkspace", { cwd });
}

export function setWorkspace(path: string): Promise<string> {
  return invoke<string>("setWorkspace", { path });
}

export function fsList(path: string | null): Promise<FsList> {
  return invoke<FsList>("fsList", { path });
}

// canPickNative reports whether the host exposes OS file dialogs
// (Electron). The e2e stub and plain web fall back to in-app pickers.
export function canPickNative(): boolean {
  try {
    return !!bridge().dialog;
  } catch {
    return false;
  }
}

// pickFiles opens the OS file picker rooted at defaultPath. Null
// means unavailable or cancelled; empty multi-select never happens.
export function pickFiles(defaultPath: string | null): Promise<string[] | null> {
  const dlg = bridge().dialog;
  if (!dlg) {
    return Promise.resolve(null);
  }
  return dlg.pickFiles(defaultPath);
}

// pickDirectory opens the OS folder picker rooted at defaultPath.
export function pickDirectory(defaultPath: string | null): Promise<string | null> {
  const dlg = bridge().dialog;
  if (!dlg) {
    return Promise.resolve(null);
  }
  return dlg.pickDirectory(defaultPath);
}

// fileComplete suggests workspace files and folders for a partial
// chat path (`@` mentions). Gitignored paths are never suggested;
// `../` queries complete from the workspace parent.
export function fileComplete(
  query: string,
  cwd: string | null,
  limit = 20,
): Promise<FileEntry[]> {
  return invoke<FileEntry[]>("fileComplete", { query, cwd, limit });
}

// listSkills discovers skills from the global config dir and the
// workspace dir. The sidecar returns name/description/source/path only
// — never SKILL.md content, which stays out of the composer and is
// injected before the LLM call.
export function listSkills(cwd: string | null): Promise<SkillEntry[]> {
  return invoke<SkillEntry[]>("listSkills", { cwd });
}

// listCommands returns the prompt commands (builtin, global, workspace).
export function listCommands(cwd: string | null): Promise<CommandEntry[]> {
  return invoke<CommandEntry[]>("listCommands", { cwd });
}

export function memoryGet(target: MemoryTarget, cwd: string | null): Promise<MemoryView> {
  return invoke<MemoryView>("memoryGet", { target, cwd });
}

export function memorySet(target: MemoryTarget, cwd: string | null, entries: string[]): Promise<MemoryView> {
  return invoke<MemoryView>("memorySet", { target, cwd, entries });
}

export function memoryProjects(): Promise<MemoryProject[]> {
  return invoke<MemoryProject[]>("memoryProjects");
}

// memoryRefreshSnapshot drops a session's frozen memory snapshot so its
// next turn sees the current memory.
export function memoryRefreshSnapshot(sessionId: string, cwd: string | null): Promise<boolean> {
  return invoke<boolean>("memoryRefreshSnapshot", { sessionId, cwd });
}

// WorktreeOpts asks for a session in a new managed git worktree; name
// seeds its branch (paladin/<slug>).
export type WorktreeOpts = { worktree?: boolean; name?: string };

export function forkSession(sessionId: string, cwd: string | null, opts: WorktreeOpts = {}): Promise<SessionInfo> {
  return invoke<SessionInfo>("forkSession", { sessionId, cwd, ...opts });
}

// revertPreview lists what reverting to the user message at index
// would do: turns dropped, files restored, files changed since.
export function revertPreview(sessionId: string, cwd: string | null, index: number): Promise<RevertPreview> {
  return invoke<RevertPreview>("revertPreview", { sessionId, cwd, index });
}

// revertSession rewinds files and hides the conversation from index on,
// until the next message makes it final or unrevertSession redoes it.
export function revertSession(sessionId: string, cwd: string | null, index: number): Promise<RevertResult> {
  return invoke<RevertResult>("revertSession", { sessionId, cwd, index });
}

export function unrevertSession(sessionId: string, cwd: string | null): Promise<unknown> {
  return invoke("unrevertSession", { sessionId, cwd });
}

export function gitInfo(cwd: string): Promise<GitInfo> {
  return invoke<GitInfo>("gitInfo", { cwd });
}

export function worktreeStatus(sessionId: string, cwd: string | null): Promise<WorktreeStatus> {
  return invoke<WorktreeStatus>("worktreeStatus", { sessionId, cwd });
}

// worktreeMerge commits the worktree's changes and merges its branch
// into the main checkout (aborting cleanly on conflicts).
export function worktreeMerge(sessionId: string, cwd: string | null, message = ""): Promise<MergeResult> {
  return invoke<MergeResult>("worktreeMerge", { sessionId, cwd, message });
}

export function renameSession(sessionId: string, cwd: string | null, title: string): Promise<boolean> {
  return invoke<boolean>("renameSession", { sessionId, cwd, title });
}

export function listProviders(): Promise<ProviderInfo[]> {
  return invoke<ProviderInfo[]>("listProviders");
}

// refreshProvider fetches one provider's model list and contacts no other.
export function refreshProvider(name: string): Promise<RefreshResult> {
  return invoke<RefreshResult>("refreshProviders", { name });
}

export function setDefaultProvider(
  name: string,
): Promise<ProviderInfo[]> {
  return invoke<ProviderInfo[]>("setDefaultProvider", { name });
}

export function addProvider(
  entry: { name: string; apiUrl: string; apiKey: string; apiType: string },
): Promise<ProviderInfo> {
  return invoke<ProviderInfo>("addProvider", { ...entry });
}

export function updateProvider(
  entry: { name: string; apiUrl: string; apiKey: string; apiType: string },
): Promise<ProviderInfo> {
  return invoke<ProviderInfo>("updateProvider", { ...entry });
}

export interface AgentPrompt {
  systemPrompt: string;
}

// getPrompt reads the agent prompt file (config/SYSTEM_PROMPT.md) for
// the Prompt settings pane. Tool guidance and examples are compiled
// per-OS in the core and always appended with the machine + workspace
// environment block.
export function getPrompt(): Promise<AgentPrompt> {
  return invoke<AgentPrompt>("getPrompt");
}

// setPrompt replaces the prompt file. Turns started afterwards use the
// new text immediately.
export function setPrompt(prompt: { systemPrompt: string }): Promise<AgentPrompt> {
  return invoke<AgentPrompt>("setPrompt", { ...prompt });
}

// resetPrompt rewrites the prompt file from the built-in default.
export function resetPrompt(): Promise<AgentPrompt> {
  return invoke<AgentPrompt>("resetPrompt");
}

export function openSession(
  sessionId: string | null,
  cwd: string | null,
  opts: WorktreeOpts = {},
): Promise<OpenedSession> {
  return invoke("openSession", { sessionId, cwd, ...opts });
}

export function listSessions(
  cwd: string | null,
  limit = 100,
): Promise<SessionInfo[]> {
  return invoke<SessionInfo[]>("listSessions", { cwd, limit });
}

export function sessionHistory(
  sessionId: string,
  cwd: string | null,
): Promise<MessageInfo[]> {
  return invoke<MessageInfo[]>("sessionHistory", { sessionId, cwd });
}

// sessionTodos reads the session's activity list (todos.json) for the
// todo hat above the composer. Empty when the agent has not planned
// yet — never an error for a missing file.
export function sessionTodos(
  sessionId: string,
  cwd: string | null,
): Promise<TodoEntry[]> {
  return invoke<TodoEntry[]>("sessionTodos", { sessionId, cwd });
}

export function sessionUsage(  sessionId: string,
  cwd: string | null,
): Promise<SessionUsage> {
  return invoke<SessionUsage>("sessionUsage", { sessionId, cwd });
}

export function overallUsage(
  cwd: string | null,
  limit = 50,
): Promise<OverallUsage> {
  return invoke<OverallUsage>("overallUsage", { cwd, limit });
}

// deleteSession removes a session; its worktree goes with it when no
// other session uses it and it is clean, or when discardWorktree is set.
export function deleteSession(
  sessionId: string,
  cwd: string | null,
  discardWorktree = false,
): Promise<boolean> {
  return invoke<boolean>("deleteSession", { sessionId, cwd, discardWorktree });
}

export type SubmitArgs = {
  sessionId: string;
  message: string;
  resume?: boolean;
  cwd: string;
  provider: string | null;
  model: string | null;
  skill: string | null;
  display: string;
  // command names a prompt command (/review, /init…) expanded by the
  // sidecar; message carries its arguments.
  command?: string | null;
  mode?: "build" | "plan";
  compact?: boolean;
  permissions?: PermissionPreset;
};

// submitChat queues a turn and returns at once: it runs now, or after
// the session's running turn (and within the concurrency cap). Its
// progress arrives on onAgentEvent.
export function submitChat(args: SubmitArgs): Promise<TurnInfo> {
  return invoke<TurnInfo>("submitChat", { ...args, systemPrompt: null });
}

// listTurns returns every queued and running turn, oldest first.
export function listTurns(): Promise<TurnInfo[]> {
  return invoke<TurnInfo[]>("turns");
}

// attachSession is the atomic history + live snapshot of a session.
export function attachSession(sessionId: string): Promise<AttachSnapshot> {
  return invoke<AttachSnapshot>("attach", { sessionId });
}

// cancelTurn drops a queued turn, or stops it if it already runs.
export function cancelTurn(turnId: string): Promise<boolean> {
  return invoke<boolean>("cancelTurn", { turnId });
}

// onAgentEvent subscribes to every turn event of every session.
export function onAgentEvent(cb: (event: TurnEvent) => void): () => void {
  const b = bridge();
  if (!b.onEvent) {
    return () => {};
  }
  return b.onEvent((msg) => cb(msg as TurnEvent));
}

// onOpenSessionRequest fires when a native notification is clicked.
export function onOpenSessionRequest(cb: (sessionId: string) => void): () => void {
  const b = bridge();
  return b.onOpenSession ? b.onOpenSession(cb) : () => {};
}

export async function takePendingOpen(): Promise<string | null> {
  const b = bridge();
  return b.takePendingOpen ? b.takePendingOpen() : null;
}

export function openExternalPage(url: string): Promise<void> {
  return bridge().shell.openExternal(url);
}

export function stopChat(
  sessionId: string | null,
  cwd: string | null,
): Promise<boolean> {
  return invoke<boolean>("stopChat", { sessionId, cwd });
}

// answerAsk delivers the wizard's answers to a blocked ask tool call.
// The turn continues on its own once the answers arrive as the tool
// result. False means nothing was waiting (the turn already moved on).
export function answerAsk(
  sessionId: string,
  callId: string,
  answers: { question: string; answer: string; selected?: string[] }[],
): Promise<boolean> {
  return invoke<boolean>("answerAsk", { sessionId, callId, answers });
}

// cancelAsk drops a blocked ask tool call and stops its turn, so the
// agent never answers its own question after the user walks away.
export function cancelAsk(
  sessionId: string | null,
  callId: string,
): Promise<boolean> {
  return invoke<boolean>("cancelAsk", { sessionId, callId });
}

// answerApproval resolves a pending approval card; the gated turn
// continues on its own. Unknown calls reject (the turn moved on).
export function answerApproval(sessionId: string, callId: string, decision: ApprovalDecision): Promise<boolean> {
  return invoke<boolean>("answerApproval", { sessionId, callId, decision });
}

export function permissionsGet(cwd: string | null, sessionId?: string | null): Promise<PermissionsView> {
  return invoke<PermissionsView>("permissionsGet", { cwd, sessionId: sessionId ?? null });
}

// permissionsSet replaces the global or one folder's saved rules.
export function permissionsSet(scope: "global" | "folder", cwd: string | null, rules: PermissionRule[]): Promise<PermissionsView> {
  return invoke<PermissionsView>("permissionsSet", { scope, cwd, rules });
}

// ---- MCP servers ----

export function mcpList(cwd: string | null): Promise<MCPOverview> {
  return invoke<MCPOverview>("mcpList", { cwd });
}

export function mcpConfig(scope: MCPScope, cwd: string | null): Promise<MCPConfigFile> {
  return invoke<MCPConfigFile>("mcpConfig", { scope, cwd });
}

// mcpSetConfig validates, writes and applies one mcp.json.
export function mcpSetConfig(scope: MCPScope, cwd: string | null, text: string): Promise<MCPOverview> {
  return invoke<MCPOverview>("mcpSetConfig", { scope, cwd, text });
}

export function mcpSetEnabled(scope: MCPScope, cwd: string | null, name: string, enabled: boolean): Promise<MCPOverview> {
  return invoke<MCPOverview>("mcpSetEnabled", { scope, cwd, name, enabled });
}

export function mcpRestart(scope: MCPScope, cwd: string | null, name: string): Promise<MCPOverview> {
  return invoke<MCPOverview>("mcpRestart", { scope, cwd, name });
}

// mcpTrust approves (or revokes) the folder's current mcp.json.
export function mcpTrust(cwd: string, trust: boolean): Promise<MCPOverview> {
  return invoke<MCPOverview>("mcpTrust", { cwd, trust });
}

// mcpParse turns a pasted command line, URL or mcp.json snippet into
// named server configs, without saving anything.
export function mcpParse(text: string): Promise<{ servers: MCPNamedServer[] }> {
  return invoke<{ servers: MCPNamedServer[] }>("mcpParse", { text });
}

// mcpAdd merges servers into one mcp.json and starts them.
export function mcpAdd(scope: MCPScope, cwd: string | null, servers: MCPNamedServer[], replace: boolean): Promise<MCPOverview> {
  return invoke<MCPOverview>("mcpAdd", { scope, cwd, servers, replace });
}

// ---- Tool switches ----

export function toolsList(cwd: string | null, sessionId: string | null): Promise<ToolsOverview> {
  return invoke<ToolsOverview>("toolsList", { cwd, sessionId });
}

// steerChat sends a message into the session's running turn; it fails
// when nothing is running (send it as a new message instead).
export function steerChat(sessionId: string, message: string, display: string): Promise<TurnInfo> {
  return invoke<TurnInfo>("steerChat", { sessionId, message, display });
}

// budgetGet reads the spend limit that applies to a session (or just
// the app default when sessionId is null).
export function budgetGet(cwd: string | null, sessionId: string | null): Promise<BudgetView> {
  return invoke<BudgetView>("budgetGet", { cwd, sessionId });
}

// budgetSet stores a USD limit globally or for one session; null
// clears it, and 0 turns a session's limit off.
export function budgetSet(scope: "global" | "session", cwd: string | null, sessionId: string | null, budgetUsd: number | null): Promise<BudgetView> {
  return invoke<BudgetView>("budgetSet", { scope, cwd, sessionId, budgetUsd });
}

// toolsSet switches tools on or off globally or for one session; null
// clears the switch.
export function toolsSet(scope: "global" | "session", cwd: string | null, sessionId: string | null, names: string[], enabled: boolean | null): Promise<ToolsOverview> {
  return invoke<ToolsOverview>("toolsSet", { scope, cwd, sessionId, names, enabled });
}

// toolsAccept lets the session use pending tools (all when names is
// empty) from its next turn.
export function toolsAccept(cwd: string | null, sessionId: string, names: string[]): Promise<ToolsOverview> {
  return invoke<ToolsOverview>("toolsAccept", { cwd, sessionId, names });
}

export function showExternalPage(
  id: string,
  url: string,
  rect: { x: number; y: number; width: number; height: number },
): Promise<unknown> {
  // Views live in the Electron main process (WebContentsView), not the
  // sidecar: ensure navigates when the URL changed, then bound and show.
  const views = bridge().views;
  return (async () => {
    await views.ensure(id, url, partitionFor(id));
    await views.setBounds(rect);
    await views.show(id);
  })();
}

export function hideExternalPage(): Promise<unknown> {
  // Main hides every view unless the name is a live page; "agent" is
  // the sentinel that shows none.
  return bridge().views.show("agent");
}

export function moveExternalPage(rect: {
  x: number;
  y: number;
  width: number;
  height: number;
}): Promise<unknown> {
  return bridge().views.setBounds(rect);
}

export function reloadExternalPage(id: string): Promise<unknown> {
  return bridge().views.reload(id);
}

export function dropExternalPage(id: string): Promise<unknown> {
  return bridge().views.drop(id);
}

// setExternalTheme keeps the native guest surfaces in step with the shell
// theme. Their default paint is white, which flashes hard in dark mode.
export function setExternalTheme(dark: boolean): Promise<unknown> {
  return bridge().views.setTheme(dark ? "dark" : "light");
}

// partitionFor names the persistent session partition for a chat page.
// Every site page shares one partition so a Google login done in one
// page (Gemini) carries over to "Sign in with Google" in another
// (ChatGPT): Electron partitions are separate cookie jars, so per-page
// partitions isolated accounts.google.com cookies from each other.
// The old per-page names (persist:paladin-gemini / -chatgpt) are
// abandoned, which costs one re-login and buys cross-page SSO.
const SHARED_SITE_PARTITION = "persist:paladin-sites";

function partitionFor(id: string): string {
  if (id.toLowerCase().startsWith("site:")) {
    return SHARED_SITE_PARTITION;
  }
  const safe = id.toLowerCase().replace(/[^a-z0-9_-]+/g, "_");
  return `persist:paladin-${safe || "page"}`;
}

// resources samples per-session process usage (cached ~1.5s in the core).
export function resources(): Promise<ResourceSnapshot> {
  return invoke<ResourceSnapshot>("resources");
}

// ---- hooks ----

export function hooksList(cwd: string | null): Promise<HooksOverview> {
  return invoke<HooksOverview>("hooksList", { cwd });
}

// hooksSetConfig validates and writes the global or folder hooks.json;
// blank text deletes it.
export function hooksSetConfig(scope: HookScope, cwd: string | null, text: string): Promise<HooksOverview> {
  return invoke<HooksOverview>("hooksSetConfig", { scope, cwd, text });
}

export function hooksTrust(cwd: string, path: string, trust: boolean): Promise<HooksOverview> {
  return invoke<HooksOverview>("hooksTrust", { cwd, path, trust });
}

export function hooksToggle(cwd: string | null, key: string, enabled: boolean): Promise<HooksOverview> {
  return invoke<HooksOverview>("hooksToggle", { cwd, key, enabled });
}

// sideQuestion asks a /btw question about a session; nothing is saved.
export function sideQuestion(sessionId: string, cwd: string | null, question: string, provider: string | null, model: string | null): Promise<SideAnswer> {
  return invoke<SideAnswer>("sideQuestion", { sessionId, cwd, question, provider, model });
}
