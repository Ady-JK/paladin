// Pure pieces of the plugin panel bridge: what a panel is granted, how fast
// it may call, and the checks around its files and URLs. Nothing here needs
// Electron's runtime, so the rules run in plain Node under Playwright.
import path from "node:path";
import type { WebFrameMain } from "electron";

type Rate = { start: number; count: number };
export type PanelGrant = {
  id: string;
  plugin: string;
  view: string;
  sessionId: string;
  workspace: string;
  permissions: Set<string>;
  inflight: number;
  reloading: boolean;
  idleWaiters: Set<() => void>;
  frame: WebFrameMain | null;
  subs: Map<string, { events: Set<string> | null; dropped: number }>;
  rates: Record<"tool" | "file" | "external" | "events" | "prompt", Rate>;
};

export const PLUGIN_EVENTS = new Set([
  "chat/delta",
  "chat/reasoning",
  "chat/approval",
  "chat/budget",
  "chat/compact",
  "chat/hook",
  "chat/protection",
  "chat/retry",
  "chat/steer",
  "chat/thinkingDropped",
  "chat/usage",
  "chat/tool",
  "turn/queued",
  "turn/start",
  "turn/done",
  "browser/navigated",
  "session/changes",
  "plugin/updated",
  "tools/pending",
]);
export const PANEL_LIMIT_PER_SESSION = 2;
// A panel call carries at most this much argument data, and reads at most one file this big.
export const MAX_TOOL_ARGS_BYTES = 256 * 1024;
export const MAX_PANEL_FILE_BYTES = 1 << 20;
export const PANEL_LIMIT_TOTAL = 4;

export function pathInside(root: string, target: string): boolean {
  const rel = path.relative(root, target);
  return rel !== "" && !rel.startsWith("..") && !path.isAbsolute(rel);
}

export function allowRate(grant: PanelGrant, key: keyof PanelGrant["rates"], max: number): boolean {
  const now = Date.now();
  const rate = grant.rates[key];
  if (now - rate.start >= 60_000) {
    rate.start = now;
    rate.count = 0;
  }
  if (rate.count >= max) return false;
  rate.count++;
  return true;
}

export function newGrant(
  id: string,
  plugin: string,
  view: string,
  sessionId: string,
  workspace: string,
  permissions: string[],
): PanelGrant {
  return {
    id,
    plugin,
    view,
    sessionId,
    workspace,
    permissions: new Set(permissions),
    inflight: 0,
    reloading: false,
    idleWaiters: new Set(),
    frame: null,
    subs: new Map(),
    rates: {
      tool: { start: Date.now(), count: 0 },
      file: { start: Date.now(), count: 0 },
      external: { start: Date.now(), count: 0 },
      events: { start: Date.now(), count: 0 },
      prompt: { start: Date.now(), count: 0 },
    },
  };
}

// describeFrame names a caller for the log: its origin and path, never the
// query (a panel id lives there).
export function describeFrame(frame: WebFrameMain | null | undefined): string {
  if (!frame) return "an unknown frame";
  try {
    const url = new URL(frame.url);
    const origin = url.origin === "null" ? `${url.protocol}//${url.host}` : url.origin;
    return `${origin}${url.pathname}`.slice(0, 200);
  } catch {
    return "a frame with no URL";
  }
}

export function pluginViewUrl(plugin: string, view: string, entry: string, id: string): string {
  const rel = entry
    .split("/")
    .map((part) => encodeURIComponent(part))
    .join("/");
  return `plugin://${plugin}/${view}/${rel}?panel=${encodeURIComponent(id)}`;
}
