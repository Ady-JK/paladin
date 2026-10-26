// Renderer API: typed wrapper over window.paladin (preload bridge).
import type { ChatResult, HistoryMessage, ProviderInfo, SessionInfo, ToolActivity } from "./types";

declare global {
  interface PaladinBridge {
    invoke: (method: string, params?: unknown) => Promise<any>;
    chat: (params: Record<string, unknown>) => Promise<string>;
    onChatEvent: (cb: (msg: any) => void) => () => void;
    onChatDone: (cb: (msg: any) => void) => () => void;
    views: {
      ensure: (name: string, url: string, partition: string) => Promise<boolean>;
      show: (name: string) => Promise<boolean>;
      setBounds: (b: { x: number; y: number; width: number; height: number }) => Promise<boolean>;
      navigate: (name: string, url: string) => Promise<boolean>;
      reload: (name: string) => Promise<boolean>;
      drop: (name: string) => Promise<boolean>;
    };
  }
  interface Window {
    paladin: PaladinBridge;
  }
}

function paladin(): PaladinBridge {
  if (!window.paladin) throw new Error("preload bridge missing (run under Electron)");
  return window.paladin;
}

export function invoke<T>(method: string, params: unknown = {}): Promise<T> {
  return paladin().invoke(method, params) as Promise<T>;
}

export const api = {
  listProviders: () => invoke<ProviderInfo[]>("list_providers"),
  refreshProviders: () => invoke<boolean>("refresh_providers"),
  setDefaultProvider: (name: string) => invoke<ProviderInfo[]>("set_default_provider", { name }),
  addProvider: (p: { name: string; apiUrl: string; apiKey: string; apiType: string }) =>
    invoke<ProviderInfo>("add_provider", p),
  updateProvider: (p: { name: string; apiUrl: string; apiKey: string; apiType: string }) =>
    invoke<ProviderInfo>("update_provider", p),
  getWorkspace: () => invoke<string>("get_workspace"),
  setWorkspace: (path: string) => invoke<string>("set_workspace", { path }),
  resolveWorkspace: (cwd: string | null) => invoke<string>("resolve_workspace", { cwd }),
  fsHome: () => invoke<string>("fs_home"),
  fsList: (path: string | null) =>
    invoke<{ currentPath: string; parentPath: string; directories: { name: string; path: string }[] }>(
      "fs_list",
      { path },
    ),
  openSession: (sessionId: string, cwd: string) =>
    invoke<{ sessionId: string; cwd: string; provider: string; model: string }>("open_session", {
      sessionId,
      cwd,
    }),
  listSessions: (cwd: string, limit = 100) =>
    invoke<SessionInfo[]>("list_sessions", { cwd, limit }),
  sessionHistory: (sessionId: string, cwd: string) =>
    invoke<HistoryMessage[]>("session_history", { sessionId, cwd }),
  deleteSession: (sessionId: string, cwd: string) =>
    invoke<boolean>("delete_session", { sessionId, cwd }),
  stopChat: (sessionId: string, cwd: string) =>
    invoke<boolean>("stop_chat", { sessionId, cwd }),
};

export interface ChatHandlers {
  onDelta: (delta: string) => void;
  onTool: (tool: ToolActivity) => void;
}

// chat() multiplexes the main-process stream onto one promise: deltas and
// tool events arrive via onChatEvent, completion via onChatDone. streamId
// isolates concurrent turns (only one runs at a time in practice).
export async function chat(
  params: Record<string, unknown>,
  handlers: ChatHandlers,
): Promise<ChatResult> {
  const bridge = paladin();
  const streamId = await bridge.chat(params);
  return new Promise<ChatResult>((resolve, reject) => {
    const offEvent = bridge.onChatEvent((msg) => {
      if (msg.streamId !== streamId) return;
      if (msg.event === "chat/delta" && typeof msg.delta === "string") handlers.onDelta(msg.delta);
      else if (msg.event === "chat/tool" && msg.tool) handlers.onTool(msg.tool as ToolActivity);
    });
    const offDone = bridge.onChatDone((msg) => {
      if (msg.streamId !== streamId) return;
      offEvent();
      offDone();
      if (msg.ok) resolve(msg.result as ChatResult);
      else reject(new Error(msg.error || "chat failed"));
    });
  });
}

export const views = {
  ensure: (name: string, url: string, partition: string) =>
    paladin().views.ensure(name, url, partition),
  show: (name: string) => paladin().views.show(name),
  setBounds: (b: { x: number; y: number; width: number; height: number }) =>
    paladin().views.setBounds(b),
  navigate: (name: string, url: string) => paladin().views.navigate(name, url),
  reload: (name: string) => paladin().views.reload(name),
  drop: (name: string) => paladin().views.drop(name),
};
