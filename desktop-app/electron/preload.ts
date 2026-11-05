// Preload: the only bridge between renderer and Electron main.
// Renderer gets window.paladin; nothing else leaks (sandbox + isolation on).

import { contextBridge, ipcRenderer } from "electron";

const listeners = new Set<(msg: unknown) => void>();
let armed = false;

// Plugin panels get their own event stream. Main sends only the events a
// frame subscribed to, so this listener is per-frame, not global.
const pluginListeners = new Set<(msg: unknown) => void>();
let pluginArmed = false;

function armPlugins(): void {
  if (pluginArmed) return;
  pluginArmed = true;
  ipcRenderer.on("plugin:event", (_e, msg: unknown) => {
    for (const cb of pluginListeners) {
      try {
        cb(msg);
      } catch (err) {
        console.error("plugin event listener failed", err);
      }
    }
  });
}

// arm installs the single agent:event listener on first subscribe and
// fans every turn event out to the onEvent subscribers.
function arm(): void {
  if (armed) return;
  armed = true;
  ipcRenderer.on("agent:event", (_e, msg: unknown) => {
    for (const cb of listeners) {
      try {
        cb(msg);
      } catch (err) {
        console.error("agent event listener failed", err);
      }
    }
  });
}

contextBridge.exposeInMainWorld("paladin", {
  invoke: (method: string, params: unknown = {}) => ipcRenderer.invoke("agent:invoke", method, params),
  core: {
    connection: () => ipcRenderer.invoke("core:connection"),
    setConnection: (value: unknown) => ipcRenderer.invoke("core:setConnection", value),
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

  // Plugin panels: the frame is untrusted; every call is checked against
  // the panel grant in main. No raw agent invoke is exposed.
  plugins: {
    open: (plugin: string, view: string, sessionId: string, workspace: string) =>
      ipcRenderer.invoke("plugins:open", { plugin, view, sessionId, workspace }),
    close: (id: string) => ipcRenderer.invoke("plugins:close", id),
    getContext: () => ipcRenderer.invoke("plugins:getContext"),
    invokeTool: (tool: string, args: unknown) => ipcRenderer.invoke("plugins:invokeTool", { tool, args }),
    readFile: (rel: string) => ipcRenderer.invoke("plugins:readFile", { rel }),
    readStore: (key: string) => ipcRenderer.invoke("plugins:readStore", { key }),
    writeStore: (key: string, value: unknown) => ipcRenderer.invoke("plugins:writeStore", { key, value }),
    openExternal: (url: string) => ipcRenderer.invoke("plugins:openExternal", { url }),
    subscribe: (filter: unknown) => ipcRenderer.invoke("plugins:subscribe", filter),
    unsubscribe: (id: string) => ipcRenderer.invoke("plugins:unsubscribe", { id }),
    onEvent: (cb: (msg: unknown) => void): (() => void) => {
      armPlugins();
      pluginListeners.add(cb);
      return () => pluginListeners.delete(cb);
    },
  },
  takePendingOpen: (): Promise<string | null> => ipcRenderer.invoke("app:takePendingOpen"),
  metrics: (): Promise<{ cpu: number; rss: number; procs: number }> => ipcRenderer.invoke("app:metrics"),

  views: {
    ensure: (name: string, url: string, partition: string) => ipcRenderer.invoke("views:ensure", name, url, partition),
    show: (name: string) => ipcRenderer.invoke("views:show", name),
    setBounds: (bounds: { x: number; y: number; width: number; height: number }) =>
      ipcRenderer.invoke("views:setBounds", bounds),
    navigate: (name: string, url: string) => ipcRenderer.invoke("views:navigate", name, url),
    reload: (name: string) => ipcRenderer.invoke("views:reload", name),
    drop: (name: string) => ipcRenderer.invoke("views:drop", name),
    setTheme: (theme: "dark" | "light") => ipcRenderer.invoke("views:setTheme", theme),
  },

  browser: {
    open: (sessionId: string, url: string) => ipcRenderer.invoke("browser:open", sessionId, url),
    show: (sessionId: string | null) => ipcRenderer.invoke("browser:show", sessionId),
    setBounds: (bounds: { x: number; y: number; width: number; height: number }) =>
      ipcRenderer.invoke("browser:setBounds", bounds),
    drop: (sessionId: string) => ipcRenderer.invoke("browser:drop", sessionId),
    pick: (sessionId: string) => ipcRenderer.invoke("browser:pick", sessionId),
    cancelPick: (sessionId: string) => ipcRenderer.invoke("browser:cancelPick", sessionId),
    getNotes: (sessionId: string) => ipcRenderer.invoke("browser:getNotes", sessionId),
    clearNotes: (sessionId: string) => ipcRenderer.invoke("browser:clearNotes", sessionId),
  },

  shell: {
    openExternal: (url: string) => ipcRenderer.invoke("shell:openExternal", url),
  },

  dialog: {
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
