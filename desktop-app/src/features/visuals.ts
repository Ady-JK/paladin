// visuals.ts draws the visual tool's SVG or HTML inline in the chat.
//
// Each visual runs in public/visual-frame.html, an iframe sandboxed to
// scripts only: it gets an opaque origin (no access to the app, its
// storage or the bridge) and its own Content-Security-Policy that allows
// no network at all, so code from the model can draw and animate but
// never load or send anything. (A srcdoc frame would inherit the app's
// policy instead.) The frame says it is ready and gets its page by
// postMessage; the page around it supplies Paladin's theme as CSS
// variables. The frame talks back only by postMessage from its own
// window, matched to its token: its height (so the card fits) and
// paladin.sendPrompt(text), which suggests a message for the composer.
// A suggestion counts only right after the user clicked inside that
// visual (the frame stamps the pointer/key gesture it saw), and never
// replaces a draft without asking. Nothing here keeps a card or frame
// alive once it leaves the page.

import { inputEl } from "../core/dom";
import { escapeHtml, tryParseJson } from "../core/format";
import type { ToolInfo } from "../core/types";
import { icon } from "../icons";

const MIN_HEIGHT = 48;
const MAX_INLINE_HEIGHT = 720;
const MAX_PROMPT = 4000;

type Theme = Record<string, string>;

// THEMES are the calm palettes the visual's variables resolve to.
const THEMES: Record<"dark" | "light", Theme> = {
  dark: {
    "--v-bg": "transparent",
    "--v-surface": "#222226",
    "--v-fg": "#e4e4e8",
    "--v-muted": "#9d9da6",
    "--v-border": "#3a3a40",
    "--v-accent": "#9286d6",
    "--v-good": "#5fb38a",
    "--v-warn": "#d9a55b",
    "--v-bad": "#d97a8c",
    "--v-series-1": "#9286d6",
    "--v-series-2": "#5fb3a8",
    "--v-series-3": "#d9a55b",
    "--v-series-4": "#6e9fd8",
    "--v-series-5": "#d97a8c",
    "--v-series-6": "#8fb36b",
  },
  light: {
    "--v-bg": "transparent",
    "--v-surface": "#f1f1f3",
    "--v-fg": "#2a2a2f",
    "--v-muted": "#7a7a83",
    "--v-border": "#cfcfd5",
    "--v-accent": "#6658b0",
    "--v-good": "#2f8a5f",
    "--v-warn": "#b37a24",
    "--v-bad": "#b5475d",
    "--v-series-1": "#6658b0",
    "--v-series-2": "#2f8a80",
    "--v-series-3": "#b37a24",
    "--v-series-4": "#3d72b8",
    "--v-series-5": "#b5475d",
    "--v-series-6": "#5f8a37",
  },
};

const FONTS =
  '--v-font: "Geist", "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; ' +
  '--v-mono: "JetBrains Mono", "Fira Code", Menlo, Monaco, Consolas, monospace;';

// CSP: inline code only, data/blob images; nothing over the network.
const CSP =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; " +
  "font-src data:; media-src data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-src 'none'";

type Visual = { title: string; code: string; caption: string; format: "svg" | "html" };

function visualOf(tool: ToolInfo): Visual | null {
  const args = tryParseJson(tool.arguments);
  if (!args || typeof args.code !== "string" || !args.code.trim()) return null;
  const code = args.code.trim();
  const head = code
    .replace(/^<\?xml[^>]*>\s*/i, "")
    .slice(0, 8)
    .toLowerCase();
  return {
    title: typeof args.title === "string" && args.title.trim() ? args.title.trim() : "Visual",
    caption: typeof args.caption === "string" ? args.caption.trim() : "",
    code,
    format: head.startsWith("<svg") ? "svg" : "html",
  };
}

function errorOf(tool: ToolInfo): string {
  const res = tryParseJson(tool.result);
  if (res && typeof res.error === "string") return res.error;
  return tool.result || "the visual could not be shown";
}

function themeName(): "dark" | "light" {
  return document.documentElement.classList.contains("light") ? "light" : "dark";
}

// visualDocument is the frame's page: CSP, theme, sizing and the bridge.
function visualDocument(v: Visual, token: string, theme: "dark" | "light"): string {
  const vars = Object.entries(THEMES[theme])
    .map(([k, val]) => `${k}: ${val};`)
    .join(" ");
  const bridge =
    `(function(){var t=${JSON.stringify(token)},g=0;function send(m){m.__paladinVisual=t;parent.postMessage(m,"*");}` +
    `function gesture(){g=Date.now();}` +
    `addEventListener("pointerdown",gesture,true);addEventListener("keydown",gesture,true);` +
    `window.paladin=Object.freeze({sendPrompt:function(text){send({type:"prompt",text:String(text).slice(0,${MAX_PROMPT}),gesture:g});},theme:${JSON.stringify(theme)}});` +
    `function size(){var h=Math.ceil(document.documentElement.getBoundingClientRect().height);send({type:"size",height:h});}` +
    `addEventListener("load",size);new ResizeObserver(size).observe(document.documentElement);` +
    `addEventListener("error",function(e){send({type:"error",message:String(e.message||e)});});})();`;
  return (
    `<!doctype html><html><head><meta charset="utf-8">` +
    `<meta http-equiv="Content-Security-Policy" content="${CSP}">` +
    `<style>:root { ${vars} ${FONTS} color-scheme: ${theme}; } ` +
    `html, body { margin: 0; background: var(--v-bg); color: var(--v-fg); font: 13px/1.5 var(--v-font); } ` +
    `body { padding: 2px; overflow: hidden; } svg { max-width: 100%; height: auto; display: block; } ` +
    `a { color: var(--v-accent); }</style>` +
    `<script>${bridge}</script></head><body>${v.code}</body></html>`
  );
}

// latest holds each card's newest call state for its buttons; pages is
// what each frame draws whenever it says it is ready (again after its
// card is put back in the page, which reloads it). Both are weak, so a
// card or frame that leaves for good is not kept.
const latest = new WeakMap<HTMLElement, ToolInfo>();
const pages = new WeakMap<HTMLIFrameElement, string>();
let listening = false;
let shownTheme = "";

function frameUrl(): string {
  return new URL("visual-frame.html", document.baseURI).href;
}

// frameOf finds the visual frame whose window sent a message.
function frameOf(source: MessageEventSource | null): HTMLIFrameElement | undefined {
  if (!source) return undefined;
  for (const frame of Array.from(document.querySelectorAll<HTMLIFrameElement>("iframe.visual-frame"))) {
    if (frame.contentWindow === source) return frame;
  }
  return undefined;
}

// Frames report through one window listener; one observer redraws the
// visuals on the page when the theme flips.
function listen(): void {
  if (listening) return;
  listening = true;
  window.addEventListener("message", (event) => {
    const frame = frameOf(event.source);
    const card = frame?.closest<HTMLElement>(".visual-card");
    const data = event.data as
      | { __paladinVisual?: string; type?: string; height?: number; text?: string; gesture?: number }
      | null;
    if (!frame || !card || !data || typeof data !== "object") return;
    if (data.type === "paladin-visual-ready") {
      const doc = pages.get(frame);
      if (doc !== undefined) frame.contentWindow?.postMessage({ type: "paladin-visual-render", doc }, "*");
      return;
    }
    if (data.__paladinVisual !== frame.dataset.token) return;
    if (data.type === "size" && typeof data.height === "number" && Number.isFinite(data.height)) {
      const max = card.classList.contains("is-expanded") ? window.innerHeight - 120 : MAX_INLINE_HEIGHT;
      frame.style.height = `${Math.max(MIN_HEIGHT, Math.min(max, data.height + 4))}px`;
    } else if (data.type === "prompt" && typeof data.text === "string" && data.text.trim()) {
      suggest(card, data.text.slice(0, MAX_PROMPT), data.gesture);
    } else if (data.type === "error") {
      card.dataset.scriptError = "true";
    }
  });
  shownTheme = themeName();
  new MutationObserver(() => {
    if (themeName() === shownTheme) return;
    shownTheme = themeName();
    for (const card of Array.from(document.querySelectorAll<HTMLElement>(".visual-card"))) {
      const now = latest.get(card);
      const v = now && visualOf(now);
      if (v && now.status === "ok") mountFrame(card, v);
    }
  }).observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
}

// suggest takes a visual's message only right after the user clicked
// inside that visual: the frame stamps the pointer or key event it saw,
// and a message on load or from a timer carries no fresh stamp. (Parent
// focus is not reliable here: clicking inside a sandboxed frame does not
// always make the frame the page's activeElement.) A draft is never
// replaced unasked.
function suggest(card: HTMLElement, text: string, gesture: unknown): void {
  const at = typeof gesture === "number" ? gesture : 0;
  if (!at || Date.now() - at > 2000) return;
  const draft = inputEl.value.trim();
  if (!draft || draft === text.trim()) {
    fillComposer(text);
    return;
  }
  const bar = card.querySelector<HTMLElement>(".visual-suggest")!;
  bar.querySelector<HTMLElement>(".visual-suggest-text")!.textContent = text;
  bar.dataset.text = text;
  bar.hidden = false;
}

function fillComposer(text: string): void {
  inputEl.value = text;
  inputEl.dispatchEvent(new Event("input", { bubbles: true }));
  inputEl.focus();
}

function token(): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function mountFrame(card: HTMLElement, v: Visual): void {
  const stage = card.querySelector<HTMLElement>(".visual-stage")!;
  stage.querySelector("iframe")?.remove();
  const frame = document.createElement("iframe");
  const t = token();
  frame.className = "visual-frame";
  frame.setAttribute("sandbox", "allow-scripts");
  frame.setAttribute("referrerpolicy", "no-referrer");
  frame.setAttribute("title", v.title);
  frame.style.height = "160px";
  // The same color scheme inside and out keeps the frame transparent;
  // a mismatch makes Chromium paint an opaque backdrop.
  frame.style.colorScheme = themeName();
  frame.dataset.token = t;
  pages.set(frame, visualDocument(v, t, themeName()));
  frame.src = frameUrl();
  stage.append(frame);
}

function download(v: Visual): void {
  const type = v.format === "svg" ? "image/svg+xml" : "text/html";
  const body = v.format === "svg" ? v.code : visualDocument(v, "saved", themeName());
  const url = URL.createObjectURL(new Blob([body], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${v.title.replace(/[^\w.-]+/g, "_").slice(0, 60) || "visual"}.${v.format}`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// paintVisual redraws a card for the call's current state.
function paintVisual(card: HTMLElement, tool: ToolInfo): void {
  const v = visualOf(tool);
  card.dataset.status = tool.status;
  const title = card.querySelector<HTMLElement>(".visual-title")!;
  const stage = card.querySelector<HTMLElement>(".visual-stage")!;
  const caption = card.querySelector<HTMLElement>(".visual-caption")!;
  title.textContent = v?.title ?? "Visual";
  card.querySelector<HTMLElement>(".visual-format")!.textContent = v ? v.format.toUpperCase() : "";
  caption.textContent = v?.caption ?? "";
  caption.hidden = !v?.caption;
  card.querySelectorAll<HTMLButtonElement>(".visual-btn").forEach((b) => (b.disabled = !v || tool.status !== "ok"));
  if (tool.status === "running" || !v) {
    stage.innerHTML = `<div class="visual-wait">${tool.status === "running" ? "Drawing…" : "No visual"}</div>`;
    card.dataset.code = "";
    return;
  }
  if (tool.status === "failed") {
    stage.innerHTML = `<div class="visual-err">${escapeHtml(errorOf(tool))}</div>`;
    card.dataset.code = "";
    return;
  }
  if (card.dataset.code === v.code && stage.querySelector("iframe")) return;
  card.dataset.code = v.code;
  stage.innerHTML = "";
  mountFrame(card, v);
  const source = card.querySelector<HTMLElement>(".visual-source")!;
  source.textContent = v.code;
}

// createVisualCard builds the card for one visual call.
export function createVisualCard(tool: ToolInfo): HTMLElement {
  listen();
  const card = document.createElement("figure");
  card.className = "visual-card";
  card.dataset.visual = tool.id;
  card.innerHTML =
    `<figcaption class="visual-head">` +
    `<span class="visual-ico">${icon("chart", "h-4 w-4")}</span>` +
    `<span class="visual-title"></span><span class="visual-format"></span>` +
    `<span class="visual-actions">` +
    `<button type="button" class="visual-btn" data-visual-expand title="Open larger" aria-label="Open larger">${icon("expand", "h-3.5 w-3.5")}</button>` +
    `<button type="button" class="visual-btn" data-visual-source title="Show source" aria-label="Show source" aria-pressed="false">${icon("code", "h-3.5 w-3.5")}</button>` +
    `<button type="button" class="visual-btn" data-visual-save title="Save as file" aria-label="Save as file">${icon("download", "h-3.5 w-3.5")}</button>` +
    `</span></figcaption>` +
    `<div class="visual-stage"></div>` +
    `<div class="visual-suggest" role="group" aria-label="Suggested message" hidden>` +
    `<span class="visual-suggest-label">Replace your draft with this visual's message?</span>` +
    `<span class="visual-suggest-text"></span>` +
    `<span class="visual-suggest-actions"><button type="button" class="visual-suggest-btn" data-visual-keep>Keep draft</button>` +
    `<button type="button" class="visual-suggest-btn is-primary" data-visual-replace>Replace draft</button></span></div>` +
    `<pre class="visual-source" hidden></pre><p class="visual-caption" hidden></p>`;
  latest.set(card, tool);
  card.addEventListener("click", (event) => {
    const t = event.target as HTMLElement;
    const bar = card.querySelector<HTMLElement>(".visual-suggest")!;
    if (t.closest("[data-visual-replace]")) {
      fillComposer(bar.dataset.text ?? "");
      bar.hidden = true;
      return;
    }
    if (t.closest("[data-visual-keep]")) {
      bar.hidden = true;
      return;
    }
    const v = visualOf(latest.get(card)!);
    if (!v) return;
    if (t.closest("[data-visual-save]")) {
      download(v);
    } else if (t.closest("[data-visual-source]")) {
      const btn = t.closest<HTMLElement>("[data-visual-source]")!;
      const pre = card.querySelector<HTMLElement>(".visual-source")!;
      pre.hidden = !pre.hidden;
      btn.setAttribute("aria-pressed", String(!pre.hidden));
    } else if (t.closest("[data-visual-expand]")) {
      setExpanded(card, !card.classList.contains("is-expanded"), v);
    }
  });
  card.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && card.classList.contains("is-expanded")) {
      const v = visualOf(latest.get(card)!);
      if (v) setExpanded(card, false, v);
    }
  });
  paintVisual(card, tool);
  return card;
}

// updateVisualCard repaints a card with a newer state of its call.
export function updateVisualCard(card: HTMLElement, tool: ToolInfo): void {
  latest.set(card, tool);
  paintVisual(card, tool);
}

function setExpanded(card: HTMLElement, on: boolean, v: Visual): void {
  card.classList.toggle("is-expanded", on);
  card.querySelector("[data-visual-expand]")?.setAttribute("aria-label", on ? "Close" : "Open larger");
  if (on) card.setAttribute("tabindex", "-1");
  mountFrame(card, v);
  if (on) card.focus();
}
