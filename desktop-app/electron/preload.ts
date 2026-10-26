// Preload: the only bridge between renderer and Electron main.
// Renderer gets window.paladin; nothing else leaks (sandbox + isolation on).

import { contextBridge, ipcRenderer } from "electron";

export interface ChatParams {
  sessionId?: string;
  message: string;
  cwd: string;
  provider?: string | null;
  model?: string | null;
  systemPrompt?: string | null;
  resume?: boolean;
}

contextBridge.exposeInMainWorld("paladin", {
  invoke: (method: string, params: unknown = {}) => ipcRenderer.invoke("agent:invoke", method, params),

  chat: (params: ChatParams) => ipcRenderer.invoke("agent:chat", params),

  onChatEvent: (cb: (msg: any) => void) => {
    const fn = (_e: unknown, msg: any) => cb(msg);
    ipcRenderer.on("agent:chat-event", fn);
    return () => ipcRenderer.removeListener("agent:chat-event", fn);
  },

  onChatDone: (cb: (msg: any) => void) => {
    const fn = (_e: unknown, msg: any) => cb(msg);
    ipcRenderer.on("agent:chat-done", fn);
    return () => ipcRenderer.removeListener("agent:chat-done", fn);
  },

  views: {
    ensure: (name: string, url: string, partition: string) =>
      ipcRenderer.invoke("views:ensure", name, url, partition),
    show: (name: string) => ipcRenderer.invoke("views:show", name),
    setBounds: (bounds: { x: number; y: number; width: number; height: number }) =>
      ipcRenderer.invoke("views:setBounds", bounds),
    navigate: (name: string, url: string) => ipcRenderer.invoke("views:navigate", name, url),
    reload: (name: string) => ipcRenderer.invoke("views:reload", name),
    drop: (name: string) => ipcRenderer.invoke("views:drop", name),
  },
});
