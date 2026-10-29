// tauri.ts probes for the native bridge.
// Under Electron the preload exposes window.paladin; under plain web
// (or e2e with a stub) it is whatever the host injected. The name stays
// so every call site keeps working unchanged.

import type { CompactionInfo, RetryInfo, ToolInfo } from "./types";

export interface ChatStreamHandlers {
  onDelta: (delta: string) => void;
  onTool: (tool: ToolInfo) => void;
  onRetry?: (retry: RetryInfo) => void;
  onCompact?: (compaction: CompactionInfo) => void;
}

export interface PaladinBridge {
  invoke: (method: string, params?: unknown) => Promise<unknown>;
  chat: (params: Record<string, unknown>, handlers: ChatStreamHandlers) => Promise<unknown>;
  // onEvent subscribes to every turn event of every session.
  onEvent?: (cb: (msg: unknown) => void) => () => void;
  // A native notification was clicked: open that session.
  onOpenSession?: (cb: (sessionId: string) => void) => () => void;
  takePendingOpen?: () => Promise<string | null>;
  // metrics sums Electron's own processes (main, renderer, GPU, ...).
  metrics?: () => Promise<{ cpu: number; rss: number; procs: number } | null>;
  views: {
    ensure: (name: string, url: string, partition: string) => Promise<boolean>;
    show: (name: string) => Promise<boolean>;
    setBounds: (bounds: { x: number; y: number; width: number; height: number }) => Promise<boolean>;
    navigate: (name: string, url: string) => Promise<boolean>;
    reload: (name: string) => Promise<boolean>;
    drop: (name: string) => Promise<boolean>;
    setTheme: (theme: "dark" | "light") => Promise<boolean>;
  };
  shell: {
    openExternal: (url: string) => Promise<void>;
  };
  dialog?: {
    pickFiles: (defaultPath: string | null) => Promise<string[] | null>;
    pickDirectory: (defaultPath: string | null) => Promise<string | null>;
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
