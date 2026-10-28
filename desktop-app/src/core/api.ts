// api.ts wraps the native backend commands.
// Single seam between the feature modules and agent-host: every method
// keeps its Tauri-era name and shape, only the transport changed —
// window.paladin (Electron preload) speaks JSONL to the sidecar.
// No daemon, no addr, no subscribe.

import type { FileEntry, FsList, MessageInfo, OverallUsage, ProviderInfo, SessionInfo, SessionUsage, SkillEntry, TodoEntry, ToolInfo } from "./types";
import { bridge } from "./tauri";

function invoke<T>(method: string, args: unknown = {}): Promise<T> {
  return bridge().invoke(method, args) as Promise<T>;
}

export function getWorkspace(): Promise<string> {
  return invoke<string>("get_workspace");
}

export function resolveWorkspace(cwd: string | null): Promise<string> {
  return invoke<string>("resolve_workspace", { cwd });
}

export function setWorkspace(path: string): Promise<string> {
  return invoke<string>("set_workspace", { path });
}

export function fsList(path: string | null): Promise<FsList> {
  return invoke<FsList>("fs_list", { path });
}

export function fsHome(): Promise<string> {
  return invoke<string>("fs_home");
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
  return invoke<FileEntry[]>("file_complete", { query, cwd, limit });
}

// listSkills discovers skills from the global config dir and the
// workspace dir. The sidecar returns name/description/source/path only
// — never SKILL.md content, which stays out of the composer and is
// injected before the LLM call.
export function listSkills(cwd: string | null): Promise<SkillEntry[]> {
  return invoke<SkillEntry[]>("list_skills", { cwd });
}

export function listProviders(): Promise<ProviderInfo[]> {
  return invoke<ProviderInfo[]>("list_providers");
}

export function refreshProviders(): Promise<boolean> {
  return invoke<boolean>("refresh_providers");
}

export function setDefaultProvider(
  name: string,
): Promise<ProviderInfo[]> {
  return invoke<ProviderInfo[]>("set_default_provider", { name });
}

export function addProvider(
  entry: { name: string; apiUrl: string; apiKey: string; apiType: string },
): Promise<ProviderInfo> {
  return invoke<ProviderInfo>("add_provider", { ...entry });
}

export function updateProvider(
  entry: { name: string; apiUrl: string; apiKey: string; apiType: string },
): Promise<ProviderInfo> {
  return invoke<ProviderInfo>("update_provider", { ...entry });
}

export interface AgentPrompt {
  systemPrompt: string;
}

// getPrompt reads the agent prompt file (config/SYSTEM_PROMPT.md) for
// the Prompt settings pane. Tool guidance and examples are compiled
// per-OS in the core and always appended with the machine + workspace
// environment block.
export function getPrompt(): Promise<AgentPrompt> {
  return invoke<AgentPrompt>("get_prompt");
}

// setPrompt replaces the prompt file. Turns started afterwards use the
// new text immediately.
export function setPrompt(prompt: { systemPrompt: string }): Promise<AgentPrompt> {
  return invoke<AgentPrompt>("set_prompt", { ...prompt });
}

// resetPrompt rewrites the prompt file from the built-in default.
export function resetPrompt(): Promise<AgentPrompt> {
  return invoke<AgentPrompt>("reset_prompt");
}

export function openSession(
  sessionId: string | null,
  cwd: string | null,
): Promise<{ sessionId: string; cwd: string; provider: string; model: string }> {
  return invoke("open_session", { sessionId, cwd });
}

export function listSessions(
  cwd: string | null,
  limit = 100,
): Promise<SessionInfo[]> {
  return invoke<SessionInfo[]>("list_sessions", { cwd, limit });
}

export function sessionHistory(
  sessionId: string,
  cwd: string | null,
): Promise<MessageInfo[]> {
  return invoke<MessageInfo[]>("session_history", { sessionId, cwd });
}

// sessionTodos reads the session's activity list (todos.json) for the
// todo hat above the composer. Empty when the agent has not planned
// yet — never an error for a missing file.
export function sessionTodos(
  sessionId: string,
  cwd: string | null,
): Promise<TodoEntry[]> {
  return invoke<TodoEntry[]>("session_todos", { sessionId, cwd });
}

export function sessionUsage(  sessionId: string,
  cwd: string | null,
): Promise<SessionUsage> {
  return invoke<SessionUsage>("session_usage", { sessionId, cwd });
}

export function overallUsage(
  cwd: string | null,
  limit = 50,
): Promise<OverallUsage> {
  return invoke<OverallUsage>("overall_usage", { cwd, limit });
}

export function deleteSession(
  sessionId: string,
  cwd: string | null,
): Promise<boolean> {
  return invoke<boolean>("delete_session", { sessionId, cwd });
}

export function chatStream(args: {
  sessionId: string | null;
  message: string;
  resume: boolean;
  cwd: string;
  provider: string | null;
  model: string | null;
  systemPrompt: string | null;
  skill: string | null;
}): Promise<{ sessionId: string; reply: string; stopped: boolean; needsContinue: boolean }> {
  // The bridge fans deltas/tools out to whoever subscribed via
  // onChatDelta/onChatTool (chat.ts), mirroring the old Tauri events.
  return bridge().chat(
    {
      sessionId: args.sessionId,
      message: args.message,
      resume: args.resume,
      cwd: args.cwd,
      provider: args.provider,
      model: args.model,
      systemPrompt: args.systemPrompt,
      skill: args.skill,
    },
    {
      onDelta: (delta) => {
        for (const cb of deltaListeners) {
          cb(delta);
        }
      },
      onTool: (tool) => {
        for (const cb of toolListeners) {
          cb(tool);
        }
      },
    },
  ) as Promise<{ sessionId: string; reply: string; stopped: boolean; needsContinue: boolean }>;
}

// onChatDelta registers a streaming-text listener (sync subscribe, the
// returned function unsubscribes). chat.ts uses it like the old
// chat://delta event.
type DeltaCb = (delta: string) => void;
type ToolCb = (tool: ToolInfo) => void;

const deltaListeners = new Set<DeltaCb>();
const toolListeners = new Set<ToolCb>();

export function onChatDelta(cb: DeltaCb): () => void {
  deltaListeners.add(cb);
  return () => {
    deltaListeners.delete(cb);
  };
}

export function onChatTool(cb: ToolCb): () => void {
  toolListeners.add(cb);
  return () => {
    toolListeners.delete(cb);
  };
}

export function openExternalPage(url: string): Promise<void> {
  return bridge().shell.openExternal(url);
}

export function stopChat(
  sessionId: string | null,
  cwd: string | null,
): Promise<boolean> {
  return invoke<boolean>("stop_chat", { sessionId, cwd });
}

// answerAsk delivers the wizard's answers to a blocked ask tool call.
// The turn continues on its own once the answers arrive as the tool
// result. False means nothing was waiting (the turn already moved on).
export function answerAsk(
  sessionId: string,
  callId: string,
  answers: { question: string; answer: string }[],
): Promise<boolean> {
  return invoke<boolean>("answer_ask", { sessionId, callId, answers });
}

// cancelAsk drops a blocked ask tool call and stops its turn, so the
// agent never answers its own question after the user walks away.
export function cancelAsk(
  sessionId: string | null,
  callId: string,
): Promise<boolean> {
  return invoke<boolean>("cancel_ask", { sessionId, callId });
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

export function navigateExternalPage(id: string, url: string): Promise<unknown> {
  return bridge().views.navigate(id, url);
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
