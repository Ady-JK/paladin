// bridge.ts probes for the native bridge.
// Under Electron the preload exposes window.paladin; in e2e runs the
// Playwright stub injects the same shape.

export type VoiceModel = {
  id: string;
  label: string;
  arch: number;
  language: string;
  description?: string;
  source: "bundled" | "user";
};

type VoiceOverview = {
  available: boolean;
  models: VoiceModel[];
  runtime?: string;
};

export type VoiceEvent = {
  event: "ready" | "line" | "stopped" | "cancelled" | "error";
  index?: number;
  id?: string;
  text?: string;
  complete?: boolean;
  latencyMs?: number;
  message?: string;
  arch?: number;
  dir?: string;
};

export interface PaladinBridge {
  invoke: (method: string, params?: unknown) => Promise<unknown>;
  core?: {
    connection: () => Promise<{ mode: "local" | "ssh"; target?: string; binary?: string }>;
    setConnection: (value: { mode: "local" | "ssh"; target?: string; binary?: string }) => Promise<unknown>;
  };
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
    snapshot: () => Promise<string | null>;
    navigate: (name: string, url: string) => Promise<boolean>;
    reload: (name: string) => Promise<boolean>;
    back: (name: string) => Promise<boolean>;
    forward: (name: string) => Promise<boolean>;
    drop: (name: string) => Promise<boolean>;
    setTheme: (theme: "dark" | "light") => Promise<boolean>;
    onState: (cb: (state: unknown) => void) => () => void;
  };
  browser?: {
    open: (sessionId: string, url: string) => Promise<unknown>;
    show: (sessionId: string | null) => Promise<boolean>;
    setBounds: (bounds: { x: number; y: number; width: number; height: number }) => Promise<boolean>;
    drop: (sessionId: string) => Promise<boolean>;
    pick: (
      sessionId: string,
    ) => Promise<{ url: string; selector: string; tag: string; label: string; text: string } | null>;
    cancelPick: (sessionId: string) => Promise<void>;
    getNotes: (
      sessionId: string,
    ) => Promise<Array<{ url: string; selector: string; tag: string; label: string; text: string; comment: string }>>;
    clearNotes: (sessionId: string) => Promise<void>;
  };
  voice?: {
    overview: () => Promise<VoiceOverview>;
    start: (modelId: string) => Promise<void>;
    audio: (samples: Float32Array) => void;
    stop: () => Promise<boolean>;
    cancel: () => Promise<boolean>;
    openFolder: () => Promise<string>;
    onEvent: (cb: (event: VoiceEvent) => void) => () => void;
  };
  shell: {
    openExternal: (url: string) => Promise<void>;
  };
  dialog?: {
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

export function hasBridge(): boolean {
  return window.paladin !== undefined;
}

export function bridge(): PaladinBridge {
  if (!window.paladin) {
    throw new Error("native bridge is missing (run under Electron)");
  }
  return window.paladin;
}
