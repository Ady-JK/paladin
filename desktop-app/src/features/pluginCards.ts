// pluginCards.ts renders a plugin's own tool result. The card's script
// runs in a per-result sandboxed iframe (srcdoc, allow-scripts only), so
// it never touches the transcript DOM or the bridge. A missing, broken
// or slow card falls back to a small text result.

import * as api from "../core/api";
import { bridge, hasBridge } from "../core/bridge";
import type { PluginCard, ToolInfo } from "../core/types";

const MAX_CARDS = 20;
const CARD_TIMEOUT_MS = 3000;
const MIN_HEIGHT = 48;
const MAX_HEIGHT = 1200;

const cards = new Map<string, PluginCard>();
const live = new Set<HTMLElement>();
const records = new WeakMap<HTMLElement, CardRecord>();

type CardRecord = {
  plugin: string;
  tool: string;
  iframe: HTMLIFrameElement | null;
  latest: ToolInfo | null;
  ready: boolean;
  timer: number;
  fallback: HTMLElement;
  sessionId: string;
};

let sweep: MutationObserver | null = null;

function track(): void {
  if (sweep) return;
  sweep = new MutationObserver(() => {
    for (const el of live) {
      if (!el.isConnected) live.delete(el);
    }
  });
  sweep.observe(document.body, { childList: true, subtree: true });
}

async function refresh(): Promise<void> {
  try {
    const result = await api.pluginViews();
    cards.clear();
    for (const card of result.cards ?? []) cards.set(card.exposed, card);
  } catch {
    cards.clear();
  }
}

export function hasPluginCard(tool: string): boolean {
  return cards.has(tool);
}

function theme(): "dark" | "light" {
  return document.documentElement.classList.contains("dark") ? "dark" : "light";
}

function resultOf(tool: ToolInfo): unknown {
  if (!tool.result) return null;
  try {
    return JSON.parse(tool.result) as unknown;
  } catch {
    return tool.result;
  }
}

function fallback(wrapper: HTMLElement, record: CardRecord, tool: ToolInfo): void {
  live.delete(wrapper);
  record.iframe?.remove();
  record.iframe = null;
  window.clearTimeout(record.timer);
  const body = wrapper.querySelector<HTMLElement>(".plugin-card-body");
  if (body && record.fallback.parentElement !== body) body.append(record.fallback);
  wrapper.style.height = "";
  const text = document.createElement("pre");
  text.textContent = tool.status === "running" ? "Running…" : tool.result || "No result";
  record.fallback.replaceChildren(text);
  wrapper.classList.add("is-fallback");
}

// createPluginCard returns a placeholder immediately; the script loads
// asynchronously and the iframe posts its height back.
export function createPluginCard(sessionId: string, tool: ToolInfo): HTMLElement {
  const wrapper = document.createElement("div");
  wrapper.className = "plugin-card";
  wrapper.dataset.card = tool.name;
  const head = document.createElement("div");
  head.className = "plugin-card-head";
  const meta = document.createElement("span");
  meta.className = "plugin-card-meta";
  meta.textContent = `${tool.name.replace(/^extension__/, "").replace("__", " / ")} · plugin card`;
  head.append(meta);
  const body = document.createElement("div");
  body.className = "plugin-card-body";
  const fallbackEl = document.createElement("div");
  fallbackEl.className = "plugin-card-fallback";
  body.append(fallbackEl);
  wrapper.append(head, body);
  const record: CardRecord = {
    plugin: "",
    tool: "",
    iframe: null,
    latest: tool,
    ready: false,
    timer: 0,
    fallback: fallbackEl,
    sessionId,
  };
  records.set(wrapper, record);
  track();

  const card = cards.get(tool.name);
  const plugins = hasBridge() ? bridge().plugins : undefined;
  if (!card || !plugins || live.size >= MAX_CARDS) {
    fallback(wrapper, record, tool);
    return wrapper;
  }
  record.plugin = card.plugin;
  record.tool = card.tool;
  live.add(wrapper);
  const iframe = document.createElement("iframe");
  iframe.className = "plugin-card-frame";
  iframe.setAttribute("sandbox", "allow-scripts");
  iframe.setAttribute("referrerpolicy", "no-referrer");
  iframe.setAttribute("title", `${card.plugin} / ${card.tool} result`);
  iframe.src = `plugin://${encodeURIComponent(card.plugin)}/__card__/${encodeURIComponent(card.tool)}/index.html`;
  record.iframe = iframe;
  fallbackEl.replaceWith(iframe);
  iframe.addEventListener("load", () => {
    record.ready = true;
    window.clearTimeout(record.timer);
    if (record.latest) paintPluginCard(wrapper, record.latest);
  });
  record.timer = window.setTimeout(() => fallback(wrapper, record, record.latest ?? tool), CARD_TIMEOUT_MS);
  if (record.latest) paintPluginCard(wrapper, record.latest);
  return wrapper;
}

// paintPluginCard pushes the newest result into the card frame. It is
// safe to call before the frame loads; the latest value is replayed.
export function paintPluginCard(wrapper: HTMLElement, tool: ToolInfo): void {
  const record = records.get(wrapper);
  if (!record) return;
  record.latest = tool;
  if (!record.iframe) {
    fallback(wrapper, record, tool);
    return;
  }
  if (!record.ready) return;
  record.iframe.contentWindow?.postMessage(
    {
      type: "paladin:card",
      result: resultOf(tool),
      ctx: { tool: tool.name, sessionId: record.sessionId, theme: theme() },
    },
    "*",
  );
}

window.addEventListener("message", (event) => {
  const data = event.data as { type?: string; height?: unknown } | null;
  if (!data || data.type !== "paladin:card-height" || typeof data.height !== "number") return;
  for (const wrapper of live) {
    if (records.get(wrapper)?.iframe?.contentWindow !== event.source) continue;
    wrapper.style.height = `${Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, Math.round(data.height)))}px`;
  }
});

export async function initPluginCards(): Promise<void> {
  await refresh();
  api.onAgentEvent((event) => {
    if (event.event === "plugin/updated") void refresh();
  });
}
