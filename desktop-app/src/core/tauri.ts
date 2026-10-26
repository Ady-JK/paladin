// tauri.ts probes for the native bridge.
// Under Electron the preload exposes window.paladin; under plain web
// (or e2e with a stub) it is whatever the host injected. The name stays
// so every call site keeps working unchanged.

import type { ToolInfo } from "./types";

export interface ChatStreamHandlers {
  onDelta: (delta: string) => void;
  onTool: (tool: ToolInfo) => void;
}

export interface PaladinBridge {
  invoke: (method: string, params?: unknown) => Promise<unknown>;
  chat: (params: Record<string, unknown>, handlers: ChatStreamHandlers) => Promise<unknown>;
  views: {
    ensure: (name: string, url: string, partition: string) => Promise<boolean>;
    show: (name: string) => Promise<boolean>;
    setBounds: (bounds: { x: number; y: number; width: number; height: number }) => Promise<boolean>;
    navigate: (name: string, url: string) => Promise<boolean>;
    reload: (name: string) => Promise<boolean>;
    drop: (name: string) => Promise<boolean>;
  };
  shell: {
    openExternal: (url: string) => Promise<void>;
  };
  windows: {
    minimize: () => Promise<void>;
    toggleMaximize: () => Promise<void>;
    close: () => Promise<void>;
    isMaximized: () => Promise<boolean>;
    onResized: (cb: () => void) => () => void;
  };
}

declare global {
  interface Window {
    paladin?: PaladinBridge;
  }
}

export function hasTauri(): boolean {
  return window.paladin !== undefined;
}

export function bridge(): PaladinBridge {
  if (!window.paladin) {
    throw new Error("native bridge is missing (run under Electron)");
  }
  return window.paladin;
}
