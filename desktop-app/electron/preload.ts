// Preload: the only bridge between renderer and Electron main.
// Renderer gets window.paladin; nothing else leaks (sandbox + isolation on).
// Chat stream demux lives here: one ipc listener pair, routed by streamId.

import { contextBridge, ipcRenderer } from "electron";

export interface ChatParams {
  sessionId: string | null;
  message: string;
  resume: boolean;
  cwd: string;
  provider: string | null;
  model: string | null;
  systemPrompt: string | null;
  skill: string | null;
}

export interface ChatHandlers {
  onDelta: (delta: string) => void;
  onTool: (tool: unknown) => void;
  onRetry?: (retry: unknown) => void;
  onCompact?: (compaction: unknown) => void;
}

const pending = new Map<string, { handlers: ChatHandlers; done: (v: unknown) => void; fail: (e: Error) => void }>();
const listeners = new Set<(msg: unknown) => void>();
let armed = false;

function arm(): void {
  if (armed) return;
  armed = true;
  // One bus for every turn event: the blocking chat() demuxes its own
  // stream by streamId; onEvent subscribers see everything.
  ipcRenderer.on("agent:event", (_e, msg: any) => {
    for (const cb of listeners) {
      try {
        cb(msg);
      } catch (err) {
        console.error("agent event listener failed", err);
      }
    }
    const p = msg?.streamId ? pending.get(msg.streamId) : undefined;
    if (!p) return;
    if (msg.event === "chat/delta" && typeof msg.delta === "string") p.handlers.onDelta(msg.delta);
    else if (msg.event === "chat/tool") p.handlers.onTool(msg.tool);
    else if (msg.event === "chat/retry") p.handlers.onRetry?.(msg.retry);
    else if (msg.event === "chat/compact") p.handlers.onCompact?.(msg.compaction);
  });
  ipcRenderer.on("agent:chat-done", (_e, msg: any) => {
    const p = pending.get(msg?.streamId);
    if (!p) return;
    pending.delete(msg.streamId);
    if (msg.ok) p.done(msg.result);
    else p.fail(new Error(typeof msg.error === "string" ? msg.error : "chat failed"));
  });
}

contextBridge.exposeInMainWorld("paladin", {
  invoke: (method: string, params: unknown = {}) => ipcRenderer.invoke("agent:invoke", method, params),

  chat: (params: ChatParams, handlers: ChatHandlers): Promise<unknown> => {
    arm();
    return ipcRenderer.invoke("agent:chat", params).then(
      (streamId) =>
        new Promise<unknown>((done, fail) => {
          pending.set(streamId as string, { handlers, done, fail });
        }),
    );
  },

  // onEvent subscribes to every turn event (chat/*, turn/*) for every
  // session; returns an unsubscribe.
  onEvent: (cb: (msg: unknown) => void): (() => void) => {
    arm();
    listeners.add(cb);
    return () => listeners.delete(cb);
  },

  // onOpenSession fires when a native notification is clicked.
  onOpenSession: (cb: (sessionId: string) => void): (() => void) => {
    const fn = (_e: unknown, id: string): void => cb(id);
    ipcRenderer.on("app:open-session", fn);
    return () => ipcRenderer.removeListener("app:open-session", fn);
  },
  takePendingOpen: (): Promise<string | null> => ipcRenderer.invoke("app:takePendingOpen"),
  metrics: (): Promise<{ cpu: number; rss: number; procs: number }> => ipcRenderer.invoke("app:metrics"),

  views: {
    ensure: (name: string, url: string, partition: string) =>
      ipcRenderer.invoke("views:ensure", name, url, partition),
    show: (name: string) => ipcRenderer.invoke("views:show", name),
    setBounds: (bounds: { x: number; y: number; width: number; height: number }) =>
      ipcRenderer.invoke("views:setBounds", bounds),
    navigate: (name: string, url: string) => ipcRenderer.invoke("views:navigate", name, url),
    reload: (name: string) => ipcRenderer.invoke("views:reload", name),
    drop: (name: string) => ipcRenderer.invoke("views:drop", name),
    setTheme: (theme: "dark" | "light") => ipcRenderer.invoke("views:setTheme", theme),
  },

  shell: {
    openExternal: (url: string) => ipcRenderer.invoke("shell:openExternal", url),
  },

  dialog: {
    pickFiles: (defaultPath: string | null) => ipcRenderer.invoke("dialog:pickFiles", defaultPath),
    pickDirectory: (defaultPath: string | null) => ipcRenderer.invoke("dialog:pickDirectory", defaultPath),
  },

  windows: {
    minimize: () => ipcRenderer.invoke("window:minimize"),
    toggleMaximize: () => ipcRenderer.invoke("window:toggleMaximize"),
    close: () => ipcRenderer.invoke("window:close"),
    isMaximized: () => ipcRenderer.invoke("window:isMaximized"),
    onResized: (cb: () => void) => {
      const fn = (): void => cb();
      ipcRenderer.on("window:resized", fn);
      return () => ipcRenderer.removeListener("window:resized", fn);
    },
  },
});
